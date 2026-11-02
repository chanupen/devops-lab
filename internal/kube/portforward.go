package kube

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// Forward maps a local port on 127.0.0.1 to a Service port.
type Forward struct {
	Local       int
	ServicePort int32
}

// PodTarget resolves a Service to one ready pod behind it and, for each
// forward, the pod port its targetPort names. A port-forward goes to a pod,
// so this is what kubectl port-forward svc/... does too.
func PodTarget(ctx context.Context, cs kubernetes.Interface, ns, svcName string, fwd []Forward) (pod string, ports []string, err error) {
	svc, err := cs.CoreV1().Services(ns).Get(ctx, svcName, metav1.GetOptions{})
	if err != nil {
		return "", nil, err
	}
	pods, err := cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: labels.SelectorFromSet(svc.Spec.Selector).String()})
	if err != nil {
		return "", nil, err
	}
	var p *corev1.Pod
	for i := range pods.Items {
		if podReady(&pods.Items[i]) {
			p = &pods.Items[i]
			break
		}
	}
	if p == nil {
		return "", nil, fmt.Errorf("no ready pod behind service %s/%s", ns, svcName)
	}
	for _, f := range fwd {
		target, err := targetPort(svc, p, f.ServicePort)
		if err != nil {
			return "", nil, err
		}
		ports = append(ports, fmt.Sprintf("%d:%d", f.Local, target))
	}
	return p.Name, ports, nil
}

func podReady(p *corev1.Pod) bool {
	if p.Status.Phase != corev1.PodRunning || p.DeletionTimestamp != nil {
		return false
	}
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// targetPort is the container port a Service port sends to.
func targetPort(svc *corev1.Service, p *corev1.Pod, port int32) (int32, error) {
	for _, sp := range svc.Spec.Ports {
		if sp.Port != port {
			continue
		}
		tp := sp.TargetPort
		switch {
		case tp.Type == intstr.Int && tp.IntVal != 0:
			return tp.IntVal, nil
		case tp.Type == intstr.Int:
			return sp.Port, nil
		}
		for _, c := range p.Spec.Containers {
			for _, cp := range c.Ports {
				if cp.Name == tp.StrVal {
					return cp.ContainerPort, nil
				}
			}
		}
		return 0, fmt.Errorf("service %s port %d: no container port named %q", svc.Name, port, tp.StrVal)
	}
	return 0, fmt.Errorf("service %s has no port %d", svc.Name, port)
}

// PortForward forwards ports ("local:pod") from 127.0.0.1 to the pod until
// ctx ends or the connection drops. ready is closed once it listens.
func PortForward(ctx context.Context, cfg *rest.Config, ns, pod string, ports []string, ready chan struct{}, out io.Writer) error {
	transport, upgrader, err := spdy.RoundTripperFor(cfg)
	if err != nil {
		return err
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return err
	}
	url := cs.CoreV1().RESTClient().Post().Resource("pods").Namespace(ns).Name(pod).SubResource("portforward").URL()
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, url)
	stop := make(chan struct{})
	go func() { <-ctx.Done(); close(stop) }()
	fw, err := portforward.NewOnAddresses(dialer, []string{"127.0.0.1"}, ports, stop, ready, io.Discard, out)
	if err != nil {
		return err
	}
	return fw.ForwardPorts()
}

// KeepForwarding re-resolves the pod and forwards again whenever the
// connection drops (the pod restarted, the laptop slept), until ctx ends.
func KeepForwarding(ctx context.Context, cfg *rest.Config, ns, svc string, fwd []Forward, log io.Writer, onReady func(pod string)) error {
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return err
	}
	backoff := time.Second
	for {
		pod, ports, err := PodTarget(ctx, cs, ns, svc, fwd)
		if err == nil {
			ready := make(chan struct{})
			go func() {
				select {
				case <-ready:
					if onReady != nil {
						onReady(pod)
					}
				case <-ctx.Done():
				}
			}()
			start := time.Now()
			err = PortForward(ctx, cfg, ns, pod, ports, ready, log)
			if time.Since(start) > time.Minute {
				backoff = time.Second
			}
		}
		if ctx.Err() != nil {
			return nil
		}
		if err == nil {
			err = errors.New("connection closed")
		}
		_, _ = fmt.Fprintf(log, "tunnel: %v; retrying in %s\n", err, backoff)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

// Ports formats forwards for messages, e.g. "8080, 8443".
func Ports(fwd []Forward) string {
	s := ""
	for i, f := range fwd {
		if i > 0 {
			s += ", "
		}
		s += strconv.Itoa(f.Local)
	}
	return s
}
