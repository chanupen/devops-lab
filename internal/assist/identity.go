package assist

import (
	"context"
	"errors"
	"fmt"
	"time"

	authv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// The tool and trace identities have separate permissions.
const (
	ToolServiceAccount  = "assist-tools"
	TraceServiceAccount = "assist-trace"
)

// MintIdentity asks Kubernetes for a short-lived service-account token. The
// operator's config is used only for TokenRequest, never for evidence calls.
func MintIdentity(ctx context.Context, operator *rest.Config, name string) (*rest.Config, error) {
	if operator == nil || (name != ToolServiceAccount && name != TraceServiceAccount) {
		return nil, errors.New("invalid assistant identity")
	}
	client, err := kubernetes.NewForConfig(operator)
	if err != nil {
		return nil, err
	}
	seconds := int64((10 * time.Minute).Seconds())
	token, err := client.CoreV1().ServiceAccounts("ai").CreateToken(ctx, name, &authv1.TokenRequest{
		Spec: authv1.TokenRequestSpec{ExpirationSeconds: &seconds},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("mint %s token: %w", name, err)
	}
	expiry := token.Status.ExpirationTimestamp.Time
	now := time.Now()
	if token.Status.Token == "" || !expiry.After(now) || expiry.After(now.Add(15*time.Minute)) {
		return nil, errors.New("token request returned an empty, expired or long-lived token")
	}
	return &rest.Config{
		Host: operator.Host,
		TLSClientConfig: rest.TLSClientConfig{
			CAFile: operator.CAFile, CAData: operator.CAData, ServerName: operator.ServerName,
		},
		BearerToken: token.Status.Token,
	}, nil
}
