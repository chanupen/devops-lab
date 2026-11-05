package kube

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// Unschedulable returns the first Pending pod (by namespace and name) that
// the scheduler has failed to place for at least grace, as
// "ns/name unschedulable for 4m: <scheduler message>". A shorter failure
// is normal: Karpenter launches a node for such a pod in a minute or two.
func Unschedulable(pods []*corev1.Pod, now time.Time, grace time.Duration) (string, bool) {
	slices.SortFunc(pods, func(a, b *corev1.Pod) int {
		return strings.Compare(a.Namespace+"/"+a.Name, b.Namespace+"/"+b.Name)
	})
	for _, p := range pods {
		if p.Status.Phase != corev1.PodPending || p.DeletionTimestamp != nil {
			continue
		}
		for _, c := range p.Status.Conditions {
			if c.Type != corev1.PodScheduled || c.Status != corev1.ConditionFalse || c.Reason != corev1.PodReasonUnschedulable {
				continue
			}
			if d := now.Sub(c.LastTransitionTime.Time); d >= grace {
				return fmt.Sprintf("pod %s/%s unschedulable for %s: %s", p.Namespace, p.Name, d.Round(time.Second), c.Message), true
			}
		}
	}
	return "", false
}

// WatchUnschedulable watches Pending pods in every namespace and returns
// the first Unschedulable report, checked every 15 seconds (the condition
// does not change while the pod waits, so events alone would not do). It
// returns "" when ctx ends.
func WatchUnschedulable(ctx context.Context, core kubernetes.Interface, grace time.Duration) string {
	f := informers.NewSharedInformerFactoryWithOptions(core, 0,
		informers.WithTweakListOptions(func(o *metav1.ListOptions) { o.FieldSelector = "status.phase=Pending" }))
	pods := f.Core().V1().Pods()
	inf, lister := pods.Informer(), pods.Lister()
	ctx, cancel := context.WithCancel(ctx)
	defer f.Shutdown() // after cancel: Shutdown waits for the informer to stop
	defer cancel()
	f.Start(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), inf.HasSynced) {
		return ""
	}
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		list, err := lister.List(labels.Everything())
		if err == nil {
			if msg, ok := Unschedulable(list, time.Now(), grace); ok {
				return msg
			}
		}
		select {
		case <-ctx.Done():
			return ""
		case <-tick.C:
		}
	}
}
