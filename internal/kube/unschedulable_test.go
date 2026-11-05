package kube

import (
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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

func TestUnschedulable(t *testing.T) {
	now := time.Date(2026, 11, 5, 18, 0, 0, 0, time.UTC)
	grace := 4 * time.Minute
	running := pendingPod("a", "running", time.Hour, now, corev1.PodReasonUnschedulable)
	running.Status.Phase = corev1.PodRunning
	deleting := pendingPod("a", "deleting", time.Hour, now, corev1.PodReasonUnschedulable)
	deleting.DeletionTimestamp = &metav1.Time{Time: now}
	tests := []struct {
		name string
		pods []*corev1.Pod
		want string // substring; "" means none
	}{
		{"none", nil, ""},
		{"Karpenter still launching a node", []*corev1.Pod{pendingPod("m", "p", time.Minute, now, corev1.PodReasonUnschedulable)}, ""},
		{"past the grace period", []*corev1.Pod{pendingPod("monitoring", "node-exporter", 5*time.Minute, now, corev1.PodReasonUnschedulable)},
			"pod monitoring/node-exporter unschedulable for 5m0s: 0/4 nodes are available: 1 Too many pods"},
		{"other reasons wait", []*corev1.Pod{pendingPod("m", "p", time.Hour, now, "SchedulingGated")}, ""},
		{"not pending", []*corev1.Pod{running}, ""},
		{"being deleted", []*corev1.Pod{deleting}, ""},
		{"first by name", []*corev1.Pod{
			pendingPod("z", "late", time.Hour, now, corev1.PodReasonUnschedulable),
			pendingPod("a", "early", time.Hour, now, corev1.PodReasonUnschedulable),
		}, "pod a/early"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Unschedulable(tt.pods, now, grace)
			if ok != (tt.want != "") || !strings.Contains(got, tt.want) {
				t.Errorf("Unschedulable = %q, %v; want %q", got, ok, tt.want)
			}
		})
	}
}
