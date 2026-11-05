package aws

import (
	"context"
	"io"
	"slices"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/chanupen/devops-lab/internal/kube"
)

// The teardown order: GitOps stops before lab deletes anything it would
// bring back, load balancers and their network interfaces go while the
// controller runs, volumes before Karpenter's nodes, and the releases last.
func TestRemoveStepsOrder(t *testing.T) {
	a := &clusterAddons{}
	var names, fatal []string
	for _, s := range a.removeSteps(&kube.Clients{}) {
		names = append(names, s.name)
		if s.fatal {
			fatal = append(fatal, s.name)
		}
	}
	want := []string{
		"stop Argo CD", "load balancers", "load balancer network interfaces", "volumes",
		"Karpenter nodes", "Karpenter node classes", "releases",
	}
	if !slices.Equal(names, want) {
		t.Errorf("steps = %v, want %v", names, want)
	}
	// What would leak once tofu deletes the cluster stops the teardown.
	if wantFatal := []string{"load balancers", "load balancer network interfaces", "volumes"}; !slices.Equal(fatal, wantFatal) {
		t.Errorf("fatal = %v, want %v", fatal, wantFatal)
	}
}

// deleteVolumes stops the workloads holding claims before it deletes the
// claims, and leaves kube-system (EBS CSI, Karpenter, the Load Balancer
// Controller) running.
func TestDeleteVolumesStopsWorkloadsFirst(t *testing.T) {
	one := int32(1)
	cs := fake.NewSimpleClientset(
		&appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "prometheus", Namespace: "monitoring"}, Spec: appsv1.StatefulSetSpec{Replicas: &one}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "operator", Namespace: "monitoring"}, Spec: appsv1.DeploymentSpec{Replicas: &one}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "ebs-csi-controller", Namespace: "kube-system"}, Spec: appsv1.DeploymentSpec{Replicas: &one}},
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "monitoring"}},
	)
	var order []string
	cs.PrependReactor("*", "*", func(act k8stesting.Action) (bool, runtime.Object, error) {
		switch act.GetVerb() {
		case "get":
			if act.GetSubresource() == "scale" {
				ga := act.(k8stesting.GetAction)
				return true, &autoscalingv1.Scale{ObjectMeta: metav1.ObjectMeta{Name: ga.GetName(), Namespace: ga.GetNamespace()},
					Spec: autoscalingv1.ScaleSpec{Replicas: 1}}, nil
			}
		case "update":
			if act.GetSubresource() == "scale" {
				s := act.(k8stesting.UpdateAction).GetObject().(*autoscalingv1.Scale)
				if s.Spec.Replicas != 0 {
					t.Errorf("scaled %s to %d", s.Name, s.Spec.Replicas)
				}
				order = append(order, "scale "+act.GetResource().Resource+" "+act.GetNamespace()+"/"+s.Name)
				return true, s, nil
			}
		case "delete":
			order = append(order, "delete "+act.GetResource().Resource+" "+act.GetNamespace()+"/"+act.(k8stesting.DeleteAction).GetName())
		}
		return false, nil, nil
	})
	if err := deleteVolumes(context.Background(), &kube.Clients{Core: cs}, io.Discard); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"scale deployments monitoring/operator",
		"scale statefulsets monitoring/prometheus",
		"delete persistentvolumeclaims monitoring/db",
	}
	if !slices.Equal(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
}
