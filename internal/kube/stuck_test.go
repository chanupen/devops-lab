package kube

import (
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var rules = StuckRules{Grace: 3 * time.Minute, Restarts: 3}

func pendingPod(ns, name string, since time.Duration, now time.Time, reason string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Status: corev1.PodStatus{Phase: corev1.PodPending, Conditions: []corev1.PodCondition{{
			Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: reason,
			LastTransitionTime: metav1.NewTime(now.Add(-since)),
			Message:            "0/4 nodes are available: 1 Too many pods",
		}}},
	}
}

func crashingPod(ns, name string, restarts int32, waiting string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{
			Name: "repo-server", RestartCount: restarts,
			State:                corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: waiting}},
			LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 137, Reason: "OOMKilled"}},
		}}},
	}
}

func readyNode(name string, status corev1.ConditionStatus, since time.Duration, now time.Time) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{
			Type: corev1.NodeReady, Status: status, Reason: "KubeletNotReady",
			LastTransitionTime: metav1.NewTime(now.Add(-since)),
			Message:            "container runtime network not ready",
		}}},
	}
}

func TestStuck(t *testing.T) {
	now := time.Date(2026, 11, 5, 18, 0, 0, 0, time.UTC)
	running := pendingPod("a", "running", time.Hour, now, corev1.PodReasonUnschedulable)
	running.Status.Phase = corev1.PodRunning
	deleting := pendingPod("a", "deleting", time.Hour, now, corev1.PodReasonUnschedulable)
	deleting.DeletionTimestamp = &metav1.Time{Time: now}
	initCrash := crashingPod("a", "init", 5, "CrashLoopBackOff")
	initCrash.Status.InitContainerStatuses, initCrash.Status.ContainerStatuses = initCrash.Status.ContainerStatuses, nil
	tests := []struct {
		name  string
		nodes []*corev1.Node
		pods  []*corev1.Pod
		want  string // substring; "" means none
	}{
		{"none", nil, nil, ""},
		{"Karpenter still launching a node", nil, []*corev1.Pod{pendingPod("m", "p", time.Minute, now, corev1.PodReasonUnschedulable)}, ""},
		{"unschedulable past grace", nil, []*corev1.Pod{pendingPod("monitoring", "node-exporter", 3*time.Minute, now, corev1.PodReasonUnschedulable)},
			"pod monitoring/node-exporter unschedulable for 3m0s: 0/4 nodes are available: 1 Too many pods"},
		{"other pending reasons wait", nil, []*corev1.Pod{pendingPod("m", "p", time.Hour, now, "SchedulingGated")}, ""},
		{"not pending", nil, []*corev1.Pod{running}, ""},
		{"being deleted", nil, []*corev1.Pod{deleting}, ""},
		{"first pod by name", nil, []*corev1.Pod{
			pendingPod("z", "late", time.Hour, now, corev1.PodReasonUnschedulable),
			pendingPod("a", "early", time.Hour, now, corev1.PodReasonUnschedulable),
		}, "pod a/early"},
		{"a crash or two at start", nil, []*corev1.Pod{crashingPod("argocd", "repo", 2, "CrashLoopBackOff")}, ""},
		{"crash looping", nil, []*corev1.Pod{crashingPod("argocd", "repo", 3, "CrashLoopBackOff")},
			"pod argocd/repo container repo-server in CrashLoopBackOff after 3 restarts; last exit 137 (OOMKilled)"},
		{"restarted but running again", nil, []*corev1.Pod{crashingPod("argocd", "repo", 6, "")}, ""},
		{"init container crash looping", nil, []*corev1.Pod{initCrash}, "pod a/init container repo-server in CrashLoopBackOff"},
		{"new node not ready yet", []*corev1.Node{readyNode("n1", corev1.ConditionFalse, time.Minute, now)}, nil, ""},
		{"node NotReady past grace", []*corev1.Node{readyNode("n1", corev1.ConditionUnknown, 4*time.Minute, now)}, nil,
			"node n1 NotReady for 4m0s: container runtime network not ready"},
		{"ready node", []*corev1.Node{readyNode("n1", corev1.ConditionTrue, time.Hour, now)}, nil, ""},
		{"nodes before pods", []*corev1.Node{readyNode("n1", corev1.ConditionFalse, time.Hour, now)},
			[]*corev1.Pod{crashingPod("a", "p", 9, "CrashLoopBackOff")}, "node n1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Stuck(tt.nodes, tt.pods, now, rules)
			if ok != (tt.want != "") || !strings.Contains(got, tt.want) {
				t.Errorf("Stuck = %q, %v; want %q", got, ok, tt.want)
			}
		})
	}
}
