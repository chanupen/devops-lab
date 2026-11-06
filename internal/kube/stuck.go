package kube

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// StuckRules say when a pod or node will not recover on its own.
type StuckRules struct {
	// Grace is how long a pod may stay unschedulable, or a node NotReady.
	// Shorter is normal: Karpenter launches a node in one to two minutes,
	// and a new node is NotReady until its CNI starts.
	Grace time.Duration
	// Restarts is how many restarts a container in CrashLoopBackOff may
	// reach. With the kubelet's 10 s doubling back-off, 3 restarts take
	// about two minutes.
	Restarts int32
}

// Stuck returns the first problem (nodes by name, then pods by namespace
// and name) as one line with the reason: a node NotReady for Grace, a
// Pending pod unschedulable for Grace, or a container in CrashLoopBackOff
// after Restarts restarts.
func Stuck(nodes []*corev1.Node, pods []*corev1.Pod, now time.Time, r StuckRules) (string, bool) {
	slices.SortFunc(nodes, func(a, b *corev1.Node) int { return strings.Compare(a.Name, b.Name) })
	for _, n := range nodes {
		if n.DeletionTimestamp != nil {
			continue
		}
		for _, c := range n.Status.Conditions {
			if c.Type != corev1.NodeReady || c.Status == corev1.ConditionTrue {
				continue
			}
			if d := now.Sub(c.LastTransitionTime.Time); d >= r.Grace {
				return fmt.Sprintf("node %s NotReady for %s: %s", n.Name, d.Round(time.Second), orReason(c.Message, c.Reason)), true
			}
		}
	}
	slices.SortFunc(pods, func(a, b *corev1.Pod) int {
		return strings.Compare(a.Namespace+"/"+a.Name, b.Namespace+"/"+b.Name)
	})
	for _, p := range pods {
		if p.DeletionTimestamp != nil {
			continue
		}
		if msg, ok := unschedulable(p, now, r.Grace); ok {
			return msg, true
		}
		for _, cs := range slices.Concat(p.Status.InitContainerStatuses, p.Status.ContainerStatuses) {
			w := cs.State.Waiting
			if w == nil || w.Reason != "CrashLoopBackOff" || cs.RestartCount < r.Restarts {
				continue
			}
			last := ""
			if t := cs.LastTerminationState.Terminated; t != nil {
				last = fmt.Sprintf("; last exit %d (%s)", t.ExitCode, t.Reason)
			}
			return fmt.Sprintf("pod %s/%s container %s in CrashLoopBackOff after %d restarts%s",
				p.Namespace, p.Name, cs.Name, cs.RestartCount, last), true
		}
	}
	return "", false
}

func unschedulable(p *corev1.Pod, now time.Time, grace time.Duration) (string, bool) {
	if p.Status.Phase != corev1.PodPending {
		return "", false
	}
	for _, c := range p.Status.Conditions {
		if c.Type != corev1.PodScheduled || c.Status != corev1.ConditionFalse || c.Reason != corev1.PodReasonUnschedulable {
			continue
		}
		if d := now.Sub(c.LastTransitionTime.Time); d >= grace {
			return fmt.Sprintf("pod %s/%s unschedulable for %s: %s", p.Namespace, p.Name, d.Round(time.Second), c.Message), true
		}
	}
	return "", false
}

func orReason(msg, reason string) string {
	if msg != "" {
		return msg
	}
	return reason
}

// WatchStuck watches nodes and pods with informers and returns the first
// Stuck report, checked every 15 seconds (a stuck state may not change, so
// events alone would not do). It returns "" when ctx ends.
func WatchStuck(ctx context.Context, core kubernetes.Interface, r StuckRules) string {
	f := informers.NewSharedInformerFactory(core, 0)
	pods, nodes := f.Core().V1().Pods(), f.Core().V1().Nodes()
	podInf, nodeInf := pods.Informer(), nodes.Informer()
	ctx, cancel := context.WithCancel(ctx)
	defer f.Shutdown() // after cancel: Shutdown waits for the informers to stop
	defer cancel()
	f.Start(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), podInf.HasSynced, nodeInf.HasSynced) {
		return ""
	}
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		ns, nerr := nodes.Lister().List(labels.Everything())
		ps, perr := pods.Lister().List(labels.Everything())
		if nerr == nil && perr == nil {
			if msg, ok := Stuck(ns, ps, time.Now(), r); ok {
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
