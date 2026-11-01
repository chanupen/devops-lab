package aws

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/chanupen/devops-lab/internal/gitops"
	"github.com/chanupen/devops-lab/internal/kube"
)

// Charts lab installs into the EKS cluster before GitOps exists: Karpenter
// must run before any workload can get a node, and the Load Balancer
// Controller's defaultTags carry expires-at, which lab extend changes and
// Git must not hold.
var (
	KarpenterChart = gitops.HelmRelease{
		Name: "karpenter", Namespace: "kube-system",
		Chart: "oci://public.ecr.aws/karpenter/karpenter", Version: "1.15.1",
	}
	LBCChart = gitops.HelmRelease{
		Name: "aws-load-balancer-controller", Namespace: "kube-system",
		RepoURL: "https://aws.github.io/eks-charts", Chart: "aws-load-balancer-controller", Version: "3.6.2",
	}
)

// Karpenter resources.
var (
	NodePoolGVR     = schema.GroupVersionResource{Group: "karpenter.sh", Version: "v1", Resource: "nodepools"}
	NodeClaimGVR    = schema.GroupVersionResource{Group: "karpenter.sh", Version: "v1", Resource: "nodeclaims"}
	EC2NodeClassGVR = schema.GroupVersionResource{Group: "karpenter.k8s.aws", Version: "v1", Resource: "ec2nodeclasses"}
)

// SpotCPULimit caps the spot NodePool: at most 8 vCPUs of spot nodes.
const SpotCPULimit = "8"

// systemNode is where lab's controllers run: the tainted system node group.
var (
	systemSelector    = map[string]any{"lab.dev/role": "system"}
	systemTolerations = []any{map[string]any{"key": "CriticalAddonsOnly", "operator": "Exists"}}
)

// ExpiresTag is the tag the TTL reaper reads.
const ExpiresTag = "expires-at"

// clusterAddons installs Karpenter, its EC2NodeClass and NodePool, and the
// Load Balancer Controller.
type clusterAddons struct {
	helm       gitops.Helm
	kubeconfig string
	cfg        aws.Config
	timeout    time.Duration
}

// Install implements inCluster. It is idempotent: lab extend runs it again
// with the new expires-at.
func (a *clusterAddons) Install(ctx context.Context, c ClusterInfo, tags map[string]string, log io.Writer) error {
	k := KarpenterChart
	k.Timeout = a.timeout
	k.Values = KarpenterValues(c)
	if err := a.helm.Ensure(ctx, k, log); err != nil {
		return fmt.Errorf("karpenter: %w", err)
	}
	clients, err := a.clients(log)
	if err != nil {
		return err
	}
	for _, obj := range []*unstructured.Unstructured{EC2NodeClass(c, tags), SpotNodePool()} {
		gvr := EC2NodeClassGVR
		if obj.GetKind() == "NodePool" {
			gvr = NodePoolGVR
		}
		if _, err := clients.Dynamic.Resource(gvr).Apply(ctx, obj.GetName(), obj,
			metav1.ApplyOptions{FieldManager: "lab", Force: true}); err != nil {
			return fmt.Errorf("apply %s %s: %w", obj.GetKind(), obj.GetName(), err)
		}
		_, _ = fmt.Fprintf(log, "applied %s %s\n", obj.GetKind(), obj.GetName())
	}
	l := LBCChart
	l.Timeout = a.timeout
	l.Values = LBCValues(c, tags)
	if err := a.helm.Ensure(ctx, l, log); err != nil {
		return fmt.Errorf("aws-load-balancer-controller: %w", err)
	}
	return nil
}

// Remove implements inCluster: load balancers first (the controller must be
// running to delete them), then Karpenter's nodes, then the releases.
func (a *clusterAddons) Remove(ctx context.Context, log io.Writer) error {
	clients, err := a.clients(log)
	if err != nil {
		return err
	}
	if err := deleteLoadBalancers(ctx, clients, a.cfg, log); err != nil {
		return err
	}
	if err := clients.Dynamic.Resource(NodePoolGVR).DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{}); err != nil {
		_, _ = fmt.Fprintf(log, "delete NodePools: %v\n", err)
	} else {
		wctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		err := kube.WaitAllGone(wctx, clients.Dynamic, NodeClaimGVR, log)
		cancel()
		if err != nil {
			_, _ = fmt.Fprintf(log, "Karpenter nodes: %v\n", err)
		}
	}
	// Deleting the EC2NodeClass lets Karpenter remove the instance profiles
	// it made for the node role; IAM cannot delete a role still in one.
	if err := clients.Dynamic.Resource(EC2NodeClassGVR).DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{}); err != nil {
		_, _ = fmt.Fprintf(log, "delete EC2NodeClasses: %v\n", err)
	} else {
		wctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		err := kube.WaitAllGone(wctx, clients.Dynamic, EC2NodeClassGVR, log)
		cancel()
		if err != nil {
			_, _ = fmt.Fprintf(log, "EC2NodeClasses: %v\n", err)
		}
	}
	return errors.Join(
		a.helm.Uninstall(ctx, LBCChart.Name, LBCChart.Namespace, log),
		a.helm.Uninstall(ctx, KarpenterChart.Name, KarpenterChart.Namespace, log),
	)
}

func (a *clusterAddons) clients(log io.Writer) (*kube.Clients, error) {
	cfg, err := kube.RESTConfig(a.kubeconfig)
	if err != nil {
		return nil, err
	}
	return kube.NewClients(kube.WarningsTo(cfg, log))
}

// deleteLoadBalancers deletes Services of type LoadBalancer and Ingresses,
// so the controller removes their load balancers, and waits until AWS has
// none left for the cluster.
func deleteLoadBalancers(ctx context.Context, c *kube.Clients, cfg aws.Config, log io.Writer) error {
	svcs, err := c.Core.CoreV1().Services("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, s := range svcs.Items {
		if s.Spec.Type == "LoadBalancer" {
			_, _ = fmt.Fprintf(log, "delete Service %s/%s (load balancer)\n", s.Namespace, s.Name)
			if err := c.Core.CoreV1().Services(s.Namespace).Delete(ctx, s.Name, metav1.DeleteOptions{}); err != nil {
				return err
			}
		}
	}
	ings, err := c.Core.NetworkingV1().Ingresses("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, i := range ings.Items {
		_, _ = fmt.Fprintf(log, "delete Ingress %s/%s\n", i.Namespace, i.Name)
		if err := c.Core.NetworkingV1().Ingresses(i.Namespace).Delete(ctx, i.Name, metav1.DeleteOptions{}); err != nil {
			return err
		}
	}
	// The controller deletes load balancers asynchronously. AWS is the
	// source of truth here: wait until no load balancer carries the
	// cluster's tag.
	deadline := time.Now().Add(5 * time.Minute)
	for {
		lbs, err := ClusterLoadBalancers(ctx, cfg, ClusterName)
		if err != nil {
			return err
		}
		if len(lbs) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%d load balancers still exist: %s", len(lbs), strings.Join(lbs, ", "))
		}
		_, _ = fmt.Fprintf(log, "%d load balancers still deleting\n", len(lbs))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}
}

// KarpenterValues are the Karpenter chart's values.
func KarpenterValues(c ClusterInfo) map[string]any {
	return map[string]any{
		"settings": map[string]any{
			"clusterName":       c.Name,
			"clusterEndpoint":   c.Endpoint,
			"interruptionQueue": c.KarpenterQueue,
		},
		// The karpenter module's Pod Identity association binds this
		// service account to the controller role.
		"serviceAccount": map[string]any{"name": "karpenter"},
		"replicas":       1,
		"nodeSelector":   systemSelector,
		"tolerations":    systemTolerations,
		"controller": map[string]any{"resources": map[string]any{
			"requests": map[string]any{"cpu": "200m", "memory": "256Mi"},
			"limits":   map[string]any{"memory": "512Mi"},
		}},
	}
}

// LBCValues are the Load Balancer Controller chart's values. defaultTags
// puts lab's tags on every load balancer, target group and security group
// it creates, so the reaper and lab down find them.
func LBCValues(c ClusterInfo, tags map[string]string) map[string]any {
	dt := map[string]any{}
	for k, v := range tags {
		dt[k] = v
	}
	dt["component"] = "load-balancer-controller" // the reaper deletes these itself
	return map[string]any{
		"clusterName":    c.Name,
		"region":         Region,
		"vpcId":          c.VPCID,
		"replicaCount":   1,
		"serviceAccount": map[string]any{"create": true, "name": "aws-load-balancer-controller"},
		"defaultTags":    dt,
		// No load balancer unless asked for: a plain type=LoadBalancer
		// Service is not turned into an NLB. lab tunnel reaches the
		// Gateway; a Service that wants an NLB sets
		// loadBalancerClass: service.k8s.aws/nlb.
		"enableServiceMutatorWebhook": false,
		"nodeSelector":                systemSelector,
		"tolerations":                 systemTolerations,
	}
}

// EC2NodeClass is how Karpenter launches nodes: AL2023, the lab's subnets
// and the cluster security group (both found by the discovery tag), IMDSv2,
// and lab's tags, which Karpenter puts on instances, volumes and launch
// templates.
func EC2NodeClass(c ClusterInfo, tags map[string]string) *unstructured.Unstructured {
	t := map[string]any{}
	for k, v := range tags {
		t[k] = v
	}
	// Not "cluster": the reaper deletes Karpenter's nodes itself, while
	// cluster resources wait for the teardown build's tofu destroy.
	t["component"] = "karpenter"
	discovery := []any{map[string]any{"tags": map[string]any{"karpenter.sh/discovery": c.Name}}}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "karpenter.k8s.aws/v1",
		"kind":       "EC2NodeClass",
		"metadata":   map[string]any{"name": "default"},
		"spec": map[string]any{
			"role":                       c.KarpenterNodeRole,
			"amiSelectorTerms":           []any{map[string]any{"alias": "al2023@latest"}},
			"subnetSelectorTerms":        discovery,
			"securityGroupSelectorTerms": discovery,
			"associatePublicIPAddress":   true, // public subnets, no NAT gateway
			"metadataOptions": map[string]any{
				"httpEndpoint": "enabled", "httpTokens": "required", "httpPutResponseHopLimit": int64(1),
			},
			"blockDeviceMappings": []any{map[string]any{
				"deviceName": "/dev/xvda",
				"ebs": map[string]any{
					"volumeSize": "20Gi", "volumeType": "gp3", "encrypted": true, "deleteOnTermination": true,
				},
			}},
			"tags": t,
		},
	}}
}

// SpotNodePool is the NodePool for workloads: arm64 spot only, at most
// SpotCPULimit vCPUs, consolidated when empty or underutilized. Drift is
// switched off: lab extend changes the EC2NodeClass tags, and that alone
// must not replace every node (lab extend retags running ones itself).
func SpotNodePool() *unstructured.Unstructured {
	req := func(key, op string, values ...string) map[string]any {
		v := make([]any, len(values))
		for i, s := range values {
			v[i] = s
		}
		return map[string]any{"key": key, "operator": op, "values": v}
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "karpenter.sh/v1",
		"kind":       "NodePool",
		"metadata":   map[string]any{"name": "spot"},
		"spec": map[string]any{
			"template": map[string]any{"spec": map[string]any{
				"nodeClassRef": map[string]any{"group": "karpenter.k8s.aws", "kind": "EC2NodeClass", "name": "default"},
				"requirements": []any{
					req("kubernetes.io/arch", "In", "arm64"),
					req("karpenter.sh/capacity-type", "In", "spot"),
					req("karpenter.k8s.aws/instance-category", "In", "c", "m", "r", "t"),
					req("karpenter.k8s.aws/instance-cpu", "Lt", "9"),
				},
				"expireAfter": "24h",
			}},
			"limits": map[string]any{"cpu": SpotCPULimit},
			"disruption": map[string]any{
				"consolidationPolicy": "WhenEmptyOrUnderutilized",
				"consolidateAfter":    "1m",
				"budgets": []any{
					map[string]any{"nodes": "0", "reasons": []any{"Drifted"}},
					map[string]any{"nodes": "1"},
				},
			},
		},
	}}
}

// WriteKubeconfig writes a kubeconfig for the EKS cluster. Tokens come from
// aws eks get-token with the lab profile, which reuses the AWS CLI's cached
// role session, so kubectl, k9s and lab itself need no extra MFA code.
func WriteKubeconfig(path string, c ClusterInfo, profile string) error {
	ca, err := base64.StdEncoding.DecodeString(c.CA)
	if err != nil {
		return fmt.Errorf("cluster CA: %w", err)
	}
	name := "lab-cloud"
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters[name] = &clientcmdapi.Cluster{Server: c.Endpoint, CertificateAuthorityData: ca}
	cfg.AuthInfos[name] = &clientcmdapi.AuthInfo{Exec: &clientcmdapi.ExecConfig{
		APIVersion: "client.authentication.k8s.io/v1beta1",
		Command:    "aws",
		Args: []string{"eks", "get-token", "--cluster-name", c.Name, "--region", Region,
			"--profile", profile, "--output", "json"},
		InteractiveMode: clientcmdapi.IfAvailableExecInteractiveMode,
	}}
	cfg.Contexts[name] = &clientcmdapi.Context{Cluster: name, AuthInfo: name}
	cfg.CurrentContext = name
	return clientcmd.WriteToFile(*cfg, path)
}

// sweepClusterInstances terminates instances Karpenter launched for the
// cluster that are still running, and waits for them, so tofu can delete
// the subnets and security group they hold.
func sweepClusterInstances(ctx context.Context, cfg aws.Config, cluster string, log io.Writer) error {
	c := ec2.NewFromConfig(cfg)
	out, err := c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{Filters: []ec2types.Filter{
		{Name: aws.String("tag:eks:eks-cluster-name"), Values: []string{cluster}},
		{Name: aws.String("tag-key"), Values: []string{"karpenter.sh/nodepool"}},
		{Name: aws.String("instance-state-name"), Values: []string{"pending", "running", "stopping", "stopped"}},
	}})
	if err != nil {
		return err
	}
	var ids []string
	for _, r := range out.Reservations {
		for _, i := range r.Instances {
			ids = append(ids, aws.ToString(i.InstanceId))
		}
	}
	if len(ids) == 0 {
		return nil
	}
	_, _ = fmt.Fprintf(log, "terminate Karpenter nodes still running: %s\n", strings.Join(ids, ", "))
	if _, err := c.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: ids}); err != nil {
		return err
	}
	return ec2.NewInstanceTerminatedWaiter(c).Wait(ctx, &ec2.DescribeInstancesInput{InstanceIds: ids}, 10*time.Minute)
}

// PublicIP is this host's public IPv4 address as AWS sees it.
func PublicIP(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://checkip.amazonaws.com", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return "", err
	}
	return parseIPv4(string(b))
}

func parseIPv4(s string) (string, error) {
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil || ip.To4() == nil {
		return "", fmt.Errorf("not an IPv4 address: %q", strings.TrimSpace(s))
	}
	return ip.String(), nil
}
