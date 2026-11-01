package kube

import (
	"context"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
)

func envoyPod(name string, ready bool) *corev1.Pod {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "envoy-gateway-system", Labels: map[string]string{"app": "envoy"}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "envoy", Ports: []corev1.ContainerPort{
			{Name: "http-80", ContainerPort: 10080}}}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: status}}},
	}
}

func TestPodTarget(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "envoy-lab", Namespace: "envoy-gateway-system"},
		Spec: corev1.ServiceSpec{Selector: map[string]string{"app": "envoy"}, Ports: []corev1.ServicePort{
			{Port: 80, TargetPort: intstr.FromString("http-80")},
			{Port: 443, TargetPort: intstr.FromInt32(10443)},
		}},
	}
	cs := fake.NewClientset(svc, envoyPod("envoy-a", false), envoyPod("envoy-b", true))
	pod, ports, err := PodTarget(context.Background(), cs, "envoy-gateway-system", "envoy-lab",
		[]Forward{{Local: 8080, ServicePort: 80}, {Local: 8443, ServicePort: 443}})
	if err != nil {
		t.Fatal(err)
	}
	// Named and numbered target ports resolve; the unready pod is passed over.
	if pod != "envoy-b" || !slices.Equal(ports, []string{"8080:10080", "8443:10443"}) {
		t.Errorf("pod %s ports %v", pod, ports)
	}
	if _, _, err := PodTarget(context.Background(), cs, "envoy-gateway-system", "envoy-lab", []Forward{{Local: 9, ServicePort: 22}}); err == nil ||
		!strings.Contains(err.Error(), "no port 22") {
		t.Errorf("unknown port: %v", err)
	}
	none := fake.NewClientset(svc, envoyPod("envoy-a", false))
	if _, _, err := PodTarget(context.Background(), none, "envoy-gateway-system", "envoy-lab", []Forward{{Local: 8080, ServicePort: 80}}); err == nil ||
		!strings.Contains(err.Error(), "no ready pod") {
		t.Errorf("no ready pod: %v", err)
	}
}
