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
	"github.com/aws/aws-sdk-go-v2/service/eks"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	// EBSCSIChart is the EBS CSI driver: gp3 volumes for Postgres and
	// Prometheus. lab installs it, not Git, because extraVolumeTags carries
	// expires-at.
	EBSCSIChart = gitops.HelmRelease{
		Name: "aws-ebs-csi-driver", Namespace: "kube-system",
		RepoURL: "https://kubernetes-sigs.github.io/aws-ebs-csi-driver", Chart: "aws-ebs-csi-driver", Version: "2.67.1",
	}
)

// ClusterCharts are the charts the cluster stack installs from the host;
// lab up prefetches them before anything is built.
func ClusterCharts() []gitops.HelmRelease {
	return []gitops.HelmRelease{KarpenterChart, LBCChart, EBSCSIChart}
}

// GP3StorageClass is the cloud lab's default StorageClass.
const GP3StorageClass = "lab-gp3"

// EBSCSIDriver is the provisioner name of the EBS CSI driver.
const EBSCSIDriver = "ebs.csi.aws.com"

// Karpenter resources.
var (
	NodePoolGVR     = schema.GroupVersionResource{Group: "karpenter.sh", Version: "v1", Resource: "nodepools"}
	NodeClaimGVR    = schema.GroupVersionResource{Group: "karpenter.sh", Version: "v1", Resource: "nodeclaims"}
	EC2NodeClassGVR = schema.GroupVersionResource{Group: "karpenter.k8s.aws", Version: "v1", Resource: "ec2nodeclasses"}
)

// MaxPods is the kubelet pod limit on Karpenter's nodes: 110, as EKS
// recommends with VPC CNI prefix delegation. There is no per-vCPU cap: 16
// per vCPU held a 1-vCPU 8 GiB node to 16 pods and made Karpenter launch a
// third spot node at 12-25% memory (session 5, 6 Nov 2026). Memory requests
// on every container decide how many pods fit, and kube-reserved memory
// on AL2023 follows the instance's ENI pod limit, not maxPods (443 MiB on
// an r8gd.medium with maxPods 16; 542 MiB on a t4g.medium with 110).
const MaxPods = 110

// SpotMinMemoryMiB is the spot NodePool's floor, exclusive: 4 GiB nodes
// (t4g.medium) and up.
const SpotMinMemoryMiB = "4000"

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
	e := EBSCSIChart
	e.Timeout = a.timeout
	e.Values = EBSCSIValues(tags)
	if err := a.helm.Ensure(ctx, e, log); err != nil {
		return fmt.Errorf("aws-ebs-csi-driver: %w", err)
	}
	return nil
}

// Remove implements inCluster. It never waits on app health: a whole
// environment goes with the cluster, so lab only releases what would
// outlive it, in the order removeSteps gives.
func (a *clusterAddons) Remove(ctx context.Context, log io.Writer) error {
	clients, err := a.clients(log)
	if err != nil {
		return err
	}
	var errs []error
	for _, s := range a.removeSteps(clients) {
		_, _ = fmt.Fprintf(log, "teardown: %s\n", s.name)
		if err := s.run(ctx, log); err != nil {
			if s.fatal {
				return fmt.Errorf("%s: %w", s.name, err)
			}
			_, _ = fmt.Fprintf(log, "%s: %v\n", s.name, err)
			errs = append(errs, fmt.Errorf("%s: %w", s.name, err))
		}
	}
	return errors.Join(errs...)
}

// removeStep is one step of Remove. A fatal step stops the teardown: going
// on would leak what it guards (a load balancer, an EBS volume) once tofu
// deletes the cluster.
type removeStep struct {
	name  string
	fatal bool
	run   func(ctx context.Context, log io.Writer) error
}

// removeSteps is the teardown order. GitOps stops first, so Argo CD does
// not recreate what lab deletes. Load balancers go while their controller
// runs, and their network interfaces with them. Stateful workloads stop so
// their claims can go, and the EBS CSI driver deletes the volumes. Then
// Karpenter's nodes, its node classes (and instance profiles), and the
// releases. tofu destroy and the EC2 sweeps after it catch the rest.
func (a *clusterAddons) removeSteps(c *kube.Clients) []removeStep {
	return []removeStep{
		{name: "stop Argo CD", run: func(ctx context.Context, log io.Writer) error { return stopGitOps(ctx, c, log) }},
		{name: "load balancers", fatal: true, run: func(ctx context.Context, log io.Writer) error {
			return deleteLoadBalancers(ctx, c, a.cfg, log)
		}},
		{name: "load balancer network interfaces", fatal: true, run: func(ctx context.Context, log io.Writer) error {
			return waitLoadBalancerENIs(ctx, a.cfg, ClusterName, log)
		}},
		{name: "volumes", fatal: true, run: func(ctx context.Context, log io.Writer) error { return deleteVolumes(ctx, c, log) }},
		{name: "Karpenter nodes", run: func(ctx context.Context, log io.Writer) error {
			if err := c.Dynamic.Resource(NodePoolGVR).DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{}); err != nil {
				return err
			}
			wctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
			defer cancel()
			return kube.WaitAllGone(wctx, c.Dynamic, NodeClaimGVR, log)
		}},
		// Deleting the EC2NodeClass lets Karpenter remove the instance
		// profiles it made for the node role; IAM cannot delete a role
		// still in one.
		{name: "Karpenter node classes", run: func(ctx context.Context, log io.Writer) error {
			if err := c.Dynamic.Resource(EC2NodeClassGVR).DeleteCollection(ctx, metav1.DeleteOptions{}, metav1.ListOptions{}); err != nil {
				return err
			}
			wctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()
			return kube.WaitAllGone(wctx, c.Dynamic, EC2NodeClassGVR, log)
		}},
		{name: "releases", run: func(ctx context.Context, log io.Writer) error {
			return errors.Join(
				a.helm.Uninstall(ctx, EBSCSIChart.Name, EBSCSIChart.Namespace, log),
				a.helm.Uninstall(ctx, LBCChart.Name, LBCChart.Namespace, log),
				a.helm.Uninstall(ctx, KarpenterChart.Name, KarpenterChart.Namespace, log),
			)
		}},
	}
}

// stopGitOps scales Argo CD's application controller to zero, so self-heal
// does not bring back the Services and claims lab deletes. A cluster
// without Argo CD is fine.
func stopGitOps(ctx context.Context, c *kube.Clients, log io.Writer) error {
	_, _ = fmt.Fprintf(log, "scale StatefulSet %s/%s to 0\n", gitops.Namespace, argoController)
	return scaleToZero(ctx, c.Core.AppsV1().StatefulSets(gitops.Namespace), argoController)
}

// argoController is Argo CD's application controller StatefulSet.
const argoController = "argocd-application-controller"

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

// deleteVolumes deletes every PersistentVolumeClaim and waits up to five
// minutes until no PersistentVolume of the EBS CSI driver is left, so its
// controller deletes the EBS volumes (reclaimPolicy Delete) while it still
// runs.
func deleteVolumes(ctx context.Context, c *kube.Clients, log io.Writer) error {
	// A claim in use is not deleted until its pod goes, so stop the
	// workloads first.
	if err := stopWorkloads(ctx, c, log); err != nil {
		return err
	}
	pvcs, err := c.Core.CoreV1().PersistentVolumeClaims("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, pvc := range pvcs.Items {
		_, _ = fmt.Fprintf(log, "delete PersistentVolumeClaim %s/%s\n", pvc.Namespace, pvc.Name)
		if err := c.Core.CoreV1().PersistentVolumeClaims(pvc.Namespace).Delete(ctx, pvc.Name, metav1.DeleteOptions{}); err != nil {
			return err
		}
	}
	deadline := time.Now().Add(5 * time.Minute)
	for {
		pvs, err := c.Core.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
		if err != nil {
			return err
		}
		var left []string
		for _, pv := range pvs.Items {
			if pv.Spec.CSI != nil && pv.Spec.CSI.Driver == EBSCSIDriver {
				left = append(left, pv.Name)
			}
		}
		if len(left) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%d EBS volumes still exist: %s", len(left), strings.Join(left, ", "))
		}
		_, _ = fmt.Fprintf(log, "%d EBS volumes still deleting\n", len(left))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}
}

// stopWorkloads scales every Deployment and StatefulSet outside
// kube-system to zero. Operators stop with them, so none scales a
// StatefulSet back up (the Prometheus operator would). kube-system keeps
// the EBS CSI driver, Karpenter and the Load Balancer Controller, which the
// rest of the teardown needs.
func stopWorkloads(ctx context.Context, c *kube.Clients, log io.Writer) error {
	apps := c.Core.AppsV1()
	deps, err := apps.Deployments("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, d := range deps.Items {
		if d.Namespace == "kube-system" || (d.Spec.Replicas != nil && *d.Spec.Replicas == 0) {
			continue
		}
		_, _ = fmt.Fprintf(log, "scale Deployment %s/%s to 0\n", d.Namespace, d.Name)
		if err := scaleToZero(ctx, apps.Deployments(d.Namespace), d.Name); err != nil {
			return err
		}
	}
	sets, err := apps.StatefulSets("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, s := range sets.Items {
		if s.Namespace == "kube-system" || (s.Spec.Replicas != nil && *s.Spec.Replicas == 0) {
			continue
		}
		_, _ = fmt.Fprintf(log, "scale StatefulSet %s/%s to 0\n", s.Namespace, s.Name)
		if err := scaleToZero(ctx, apps.StatefulSets(s.Namespace), s.Name); err != nil {
			return err
		}
	}
	return nil
}

// scaler is the scale subresource of Deployments and StatefulSets.
type scaler interface {
	GetScale(ctx context.Context, name string, opts metav1.GetOptions) (*autoscalingv1.Scale, error)
	UpdateScale(ctx context.Context, name string, scale *autoscalingv1.Scale, opts metav1.UpdateOptions) (*autoscalingv1.Scale, error)
}

func scaleToZero(ctx context.Context, s scaler, name string) error {
	sc, err := s.GetScale(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	sc.Spec.Replicas = 0
	_, err = s.UpdateScale(ctx, name, sc, metav1.UpdateOptions{})
	return err
}

// waitLoadBalancerENIs waits until the cluster's VPC holds no load
// balancer network interface ("ELB ..."): AWS removes them some time
// after the load balancer, and tofu cannot delete subnets that hold one.
func waitLoadBalancerENIs(ctx context.Context, cfg aws.Config, cluster string, log io.Writer) error {
	out, err := eks.NewFromConfig(cfg).DescribeCluster(ctx, &eks.DescribeClusterInput{Name: aws.String(cluster)})
	if err != nil {
		return err
	}
	if out.Cluster == nil || out.Cluster.ResourcesVpcConfig == nil || out.Cluster.ResourcesVpcConfig.VpcId == nil {
		return nil
	}
	vpc := *out.Cluster.ResourcesVpcConfig.VpcId
	c := ec2.NewFromConfig(cfg)
	deadline := time.Now().Add(5 * time.Minute)
	for {
		enis, err := c.DescribeNetworkInterfaces(ctx, &ec2.DescribeNetworkInterfacesInput{Filters: []ec2types.Filter{
			{Name: aws.String("vpc-id"), Values: []string{vpc}},
			{Name: aws.String("description"), Values: []string{"ELB *"}},
		}})
		if err != nil {
			return err
		}
		if len(enis.NetworkInterfaces) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%d load balancer network interfaces still in %s", len(enis.NetworkInterfaces), vpc)
		}
		_, _ = fmt.Fprintf(log, "%d load balancer network interfaces still deleting\n", len(enis.NetworkInterfaces))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}
}

// EBSCSIValues are the EBS CSI driver chart's values: one controller on the
// system node, lab's tags on every volume it creates (so the reaper and the
// leftovers check see any left behind), and lab-gp3 as the default
// StorageClass: gp3, encrypted, deleted with its claim.
func EBSCSIValues(tags map[string]string) map[string]any {
	vt := map[string]any{}
	for k, v := range tags {
		vt[k] = v
	}
	return map[string]any{
		"controller": map[string]any{
			"replicaCount":    1,
			"extraVolumeTags": vt,
			"nodeSelector":    systemSelector,
			"tolerations":     systemTolerations,
			"serviceAccount":  map[string]any{"create": true, "name": "ebs-csi-controller-sa"},
		},
		"storageClasses": []any{map[string]any{
			"name":                 GP3StorageClass,
			"annotations":          map[string]any{"storageclass.kubernetes.io/is-default-class": "true"},
			"volumeBindingMode":    "WaitForFirstConsumer",
			"reclaimPolicy":        "Delete",
			"allowVolumeExpansion": true,
			"parameters":           map[string]any{"type": "gp3", "encrypted": "true"},
		}},
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

// ArgoCDValues are merged over lab's Argo CD chart values in cloud mode:
// memory requests near what each component uses on the local lab
// (kubectl top), so Karpenter sizes nodes for them. Without requests it
// packed Argo CD onto a t4g.micro (session 4). repo-server renders Helm
// charts, so its limit leaves room above its idle use.
func ArgoCDValues() map[string]any {
	res := func(cpu, req, limit string) map[string]any {
		return map[string]any{"resources": map[string]any{
			"requests": map[string]any{"cpu": cpu, "memory": req},
			"limits":   map[string]any{"memory": limit},
		}}
	}
	return map[string]any{
		"controller":     res("50m", "640Mi", "1280Mi"), // uses 588Mi
		"repoServer":     res("20m", "128Mi", "512Mi"),  // uses 65Mi
		"server":         res("10m", "64Mi", "256Mi"),   // uses 29Mi
		"applicationSet": res("10m", "64Mi", "192Mi"),   // uses 38Mi
		"redis":          res("10m", "32Mi", "128Mi"),   // uses 15Mi
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
			// The VPC CNI runs with prefix delegation, which Karpenter does
			// not detect: without maxPods, max pods follows the ENI limit (8
			// on an r8gd.medium). No podsPerCore: see MaxPods.
			"kubelet": map[string]any{"maxPods": int64(MaxPods)},
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
					// At least 4 GiB. With prefix delegation a t4g.micro (1 GiB)
					// takes 32 pods; Karpenter packed Argo CD onto one by its
					// small requests and the node ran out of memory.
					req("karpenter.k8s.aws/instance-memory", "Gt", SpotMinMemoryMiB),
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

// sweepClusterVolumes deletes EBS volumes the CSI driver made for the lab
// that are still there and unattached once the cluster is gone (a claim
// whose volume the driver did not delete in time).
func sweepClusterVolumes(ctx context.Context, cfg aws.Config, log io.Writer) error {
	c := ec2.NewFromConfig(cfg)
	out, err := c.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{Filters: []ec2types.Filter{
		{Name: aws.String("tag:lab"), Values: []string{"true"}},
		{Name: aws.String("tag-key"), Values: []string{"ebs.csi.aws.com/cluster"}},
		{Name: aws.String("status"), Values: []string{"available"}},
	}})
	if err != nil {
		return err
	}
	var errs []error
	for _, v := range out.Volumes {
		id := aws.ToString(v.VolumeId)
		_, _ = fmt.Fprintf(log, "delete EBS volume %s (left by the CSI driver)\n", id)
		if _, err := c.DeleteVolume(ctx, &ec2.DeleteVolumeInput{VolumeId: v.VolumeId}); err != nil {
			errs = append(errs, fmt.Errorf("delete volume %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
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
