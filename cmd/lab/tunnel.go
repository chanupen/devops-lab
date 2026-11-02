package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os/signal"
	"path/filepath"
	"syscall"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/spf13/cobra"

	"github.com/chanupen/devops-lab/internal/gitops"
	"github.com/chanupen/devops-lab/internal/kube"
	"github.com/chanupen/devops-lab/internal/provider/local"
)

// The lab Gateway (gitops/stacks/core/gateway) and the Service Envoy
// Gateway makes for it.
const (
	gatewayName      = "lab"
	gatewayNamespace = "envoy-gateway-system"
)

func newTunnelCmd() *cobra.Command {
	var (
		flags             labFlags
		httpPort, tlsPort int
	)
	cmd := &cobra.Command{
		Use:   "tunnel",
		Short: "Reach a cloud lab's apps at https://<app>.localtest.me:8443 without a load balancer",
		Long: "Port-forwards 127.0.0.1:8080 and :8443 on this host to the lab Gateway's Envoy\n" +
			"Service, so a cloud lab's apps have the same URLs as a local one, with no load\n" +
			"balancer to pay for. From the Mac, ssh -N " + local.TunnelHost + " forwards those\n" +
			"ports as before. Runs until Ctrl-C and reconnects when the Envoy pod changes.\n" +
			"A local lab already publishes 8080 and 8443, so use other ports beside one.",
		Example: "  lab tunnel\n  lab tunnel --http-port 9080 --https-port 9443",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if flags.mode == "" {
				flags.mode = "cloud"
			}
			if flags.mode != "cloud" {
				return errors.New("a local lab needs no tunnel: kind publishes 8080 and 8443 already")
			}
			_, store, _, err := flags.load()
			if err != nil {
				return err
			}
			st, err := store.Load()
			if err != nil {
				return err
			}
			if !st.IsApplied(gatewayStack) {
				return fmt.Errorf("the cloud lab has no Gateway yet: the %s stack is not applied", gatewayStack)
			}
			fwd := []kube.Forward{{Local: httpPort, ServicePort: 80}, {Local: tlsPort, ServicePort: 443}}
			for _, f := range fwd {
				if err := portFree(f.Local); err != nil {
					return err
				}
			}
			cfg, err := kube.RESTConfig(filepath.Join(homeOf(store), "kubeconfig"))
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			c, err := kube.NewClients(cfg)
			if err != nil {
				return err
			}
			svc, err := envoyService(ctx, c)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintln(out, "$ lab tunnel")
			return kube.KeepForwarding(ctx, cfg, gatewayNamespace, svc, fwd, cmd.ErrOrStderr(), func(pod string) {
				_, _ = fmt.Fprintf(out, "forwarding 127.0.0.1:%s to %s/%s (pod %s); Ctrl-C stops\n", kube.Ports(fwd), gatewayNamespace, svc, pod)
				printTunnelURLs(ctx, out, c, tlsPort)
			})
		},
	}
	flags.register(cmd)
	flags.registerMode(cmd)
	cmd.Flags().IntVar(&httpPort, "http-port", local.GatewayHTTPHostPort, "local port for HTTP")
	cmd.Flags().IntVar(&tlsPort, "https-port", local.GatewayHTTPSHostPort, "local port for HTTPS")
	return cmd
}

// envoyService finds the Service Envoy Gateway created for the lab Gateway.
func envoyService(ctx context.Context, c *kube.Clients) (string, error) {
	sel := labels.SelectorFromSet(map[string]string{
		"gateway.envoyproxy.io/owning-gateway-name":      gatewayName,
		"gateway.envoyproxy.io/owning-gateway-namespace": gatewayNamespace,
	}).String()
	list, err := c.Core.CoreV1().Services(gatewayNamespace).List(ctx, metav1.ListOptions{LabelSelector: sel})
	if err != nil {
		return "", err
	}
	if len(list.Items) == 0 {
		return "", fmt.Errorf("no Envoy Service for Gateway %s/%s yet", gatewayNamespace, gatewayName)
	}
	return list.Items[0].Name, nil
}

// portFree refuses a port something else listens on, such as a local lab.
func portFree(port int) error {
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("127.0.0.1:%d is in use (a local lab publishes 8080 and 8443): pick others with --http-port/--https-port", port)
	}
	return l.Close()
}

func printTunnelURLs(ctx context.Context, w io.Writer, c *kube.Clients, tlsPort int) {
	routes, err := c.Dynamic.Resource(gitops.HTTPRouteGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		_, _ = fmt.Fprintf(w, "could not list apps: %v\n", err)
		return
	}
	for _, h := range gitops.RouteHosts(routes.Items) {
		_, _ = fmt.Fprintf(w, "  https://%s:%d\n", h, tlsPort)
	}
}
