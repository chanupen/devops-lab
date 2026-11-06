package local

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// The ai stack in local mode. Ollama is not a lab container: it is the
// host's Quadlet user service (~/.config/containers/systemd/ollama.container),
// which owns the GPU. lab never edits that file. The Quadlet attaches the
// container to the kind network at a fixed address (Network=kind:ip=...)
// and publishes its port on 127.0.0.1 only, so pods reach it like lab-zot
// and lab-moto and nothing listens on the LAN. In the cluster it is
// ollama.ai.svc:11434: a Service without a selector (from Git) and an
// EndpointSlice lab writes here, because Argo CD does not manage
// EndpointSlices.
const (
	OllamaContainer = "ollama"
	OllamaAlias     = "lab-ollama"
	OllamaPort      = 11434
	// OllamaModel is the model the smoke test and lab doctor expect: a 3B
	// model in 4-bit fits the GTX 1650's 4 GB.
	OllamaModel = "llama3.2:3b"
	// OllamaQuadlet is the unit file the host's Ollama runs from.
	OllamaQuadlet = "~/.config/containers/systemd/ollama.container"
	// OllamaHostEndpoint is Ollama from the host.
	OllamaHostEndpoint = "http://127.0.0.1:11434"
	// AINamespace holds the ai stack (the gitops package has the same name).
	AINamespace = "ai"
	// OllamaService and OllamaSlice are ollama.ai.svc and the EndpointSlice
	// lab writes for it.
	OllamaService = "ollama"
	OllamaSlice   = "ollama-host"
	// LangfuseBucket keeps Langfuse's raw events in Moto's S3.
	LangfuseBucket = "lab-langfuse"
)

// Langfuse's secrets in Secrets Manager. Each is one JSON object that an
// ExternalSecret in gitops/stacks/ai/secrets reads whole, with the key names
// the Langfuse chart expects.
const (
	LangfuseAppSecret        = "lab/ai/langfuse-app"
	LangfusePostgresSecret   = "lab/ai/langfuse-postgresql"
	LangfuseRedisSecret      = "lab/ai/langfuse-redis"
	LangfuseClickHouseSecret = "lab/ai/langfuse-clickhouse"
)

// OllamaQuadletLines is what the Quadlet needs for the ai stack, for error
// messages: lab shows it and never writes the file.
func OllamaQuadletLines(addr string) string {
	if addr == "" {
		addr = "<a free address in the kind network>"
	}
	return fmt.Sprintf("Network=%s:ip=%s\nNetworkAlias=%s\nPublishPort=127.0.0.1:%d:%d", KindNetwork, addr, OllamaAlias, OllamaPort, OllamaPort)
}

// OllamaAddress checks the host's Ollama container for the ai stack and
// returns its IPv4 address on the kind network. It refuses a container
// published on anything but loopback (Ollama has no auth) and one that is
// not on the kind network, with the Quadlet lines that fix it.
func (p *Provider) OllamaAddress(ctx context.Context) (string, error) {
	return ollamaAddress(ctx, p.rt)
}

func ollamaAddress(ctx context.Context, rt Runtime) (string, error) {
	info, err := rt.Container(ctx, OllamaContainer)
	if err != nil {
		return "", err
	}
	if !info.Exists {
		return "", fmt.Errorf("no %s container: the ai stack uses the host's Ollama, the Quadlet service %s (docs/design.md, GPU setup)",
			OllamaContainer, OllamaQuadlet)
	}
	if !info.Running {
		return "", fmt.Errorf("%s is stopped: systemctl --user start ollama", OllamaContainer)
	}
	if exposed := NonLoopback(info.HostPorts); len(exposed) > 0 {
		return "", fmt.Errorf("%s is published on %s, which the LAN can reach, and it has no auth: set PublishPort=127.0.0.1:%d:%d in %s, "+
			"then systemctl --user daemon-reload && systemctl --user restart ollama", OllamaContainer, strings.Join(exposed, ", "),
			OllamaPort, OllamaPort, OllamaQuadlet)
	}
	addr := info.Networks[KindNetwork]
	if addr == "" {
		return "", fmt.Errorf("%s is not on the %s network, so pods cannot reach it. Add to [Container] in %s:\n%s\n"+
			"then systemctl --user daemon-reload && systemctl --user restart ollama", OllamaContainer, KindNetwork, OllamaQuadlet,
			OllamaQuadletLines(suggestOllamaAddress(ctx, rt)))
	}
	return addr, nil
}

// suggestOllamaAddress is .250 of the kind network's IPv4 subnet, read from
// lab-zot's address, or "" when there is nothing to read it from.
func suggestOllamaAddress(ctx context.Context, rt Runtime) string {
	zot, err := rt.Container(ctx, ZotContainer)
	if err != nil {
		return ""
	}
	ip := net.ParseIP(zot.Networks[KindNetwork]).To4()
	if ip == nil {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d.250", ip[0], ip[1], ip[2])
}

// NonLoopback lists the published host addresses, from
// ContainerInfo.HostPorts, that are not loopback ("0.0.0.0:11434" and an
// empty host IP both mean every address).
func NonLoopback(ports map[string][]string) []string {
	var out []string
	for _, binds := range ports {
		for _, b := range binds {
			host, _, err := net.SplitHostPort(b)
			if err != nil {
				host = b
			}
			if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
				out = append(out, b)
			}
		}
	}
	return out
}

// WriteOllamaEndpoints points ollama.ai.svc at addr: the ai namespace (if
// Argo CD has not made it yet) and the EndpointSlice ollama-host. The
// Service itself comes from Git.
func (p *Provider) WriteOllamaEndpoints(ctx context.Context, addr string, log io.Writer) error {
	c, err := p.kube()
	if err != nil {
		return err
	}
	return writeOllamaEndpoints(ctx, c.Core, addr, log)
}

func writeOllamaEndpoints(ctx context.Context, core kubernetes.Interface, addr string, log io.Writer) error {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: AINamespace}}
	if _, err := core.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	want := OllamaEndpointSlice(addr)
	slices := core.DiscoveryV1().EndpointSlices(AINamespace)
	cur, err := slices.Get(ctx, OllamaSlice, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		logf(log, "pointing %s.%s.svc at %s:%d (EndpointSlice %s)", OllamaService, AINamespace, addr, OllamaPort, OllamaSlice)
		_, err = slices.Create(ctx, want, metav1.CreateOptions{})
		return err
	case err != nil:
		return err
	}
	if got := SliceAddress(cur); got == addr {
		logf(log, "%s.%s.svc already points at %s:%d", OllamaService, AINamespace, addr, OllamaPort)
		return nil
	}
	logf(log, "pointing %s.%s.svc at %s:%d (was %s)", OllamaService, AINamespace, addr, OllamaPort, SliceAddress(cur))
	cur.Labels, cur.AddressType, cur.Endpoints, cur.Ports = want.Labels, want.AddressType, want.Endpoints, want.Ports
	_, err = slices.Update(ctx, cur, metav1.UpdateOptions{})
	return err
}

// OllamaEndpointSlice is the EndpointSlice behind ollama.ai.svc.
func OllamaEndpointSlice(addr string) *discoveryv1.EndpointSlice {
	ready := true
	return &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{Name: OllamaSlice, Namespace: AINamespace, Labels: map[string]string{
			discoveryv1.LabelServiceName: OllamaService,
			// Not the EndpointSlice controller's name, so it leaves this one alone.
			discoveryv1.LabelManagedBy: "lab.devops",
		}},
		AddressType: discoveryv1.AddressTypeIPv4,
		Endpoints:   []discoveryv1.Endpoint{{Addresses: []string{addr}, Conditions: discoveryv1.EndpointConditions{Ready: &ready}}},
		Ports:       []discoveryv1.EndpointPort{{Name: aws.String("http"), Port: aws.Int32(OllamaPort), Protocol: ptr(corev1.ProtocolTCP)}},
	}
}

// SliceAddress is the first address of an EndpointSlice, or "".
func SliceAddress(s *discoveryv1.EndpointSlice) string {
	if s == nil || len(s.Endpoints) == 0 || len(s.Endpoints[0].Addresses) == 0 {
		return ""
	}
	return s.Endpoints[0].Addresses[0]
}

func ptr[T any](v T) *T { return &v }

// SeedAI creates what the ai stack needs in Moto before its apps sync:
// Langfuse's secrets, with random values, unless they exist, and the S3
// bucket for its events.
func (p *Provider) SeedAI(ctx context.Context, log io.Writer) error {
	if err := p.motoReady(ctx, log); err != nil {
		return err
	}
	for _, s := range langfuseSecrets() {
		created, err := SeedJSON(ctx, p.secrets, s.name, s.gen)
		if err != nil {
			return fmt.Errorf("seed %s: %w", s.name, err)
		}
		if created {
			logf(log, "created %s in Secrets Manager (%s)", s.name, MotoContainer)
		} else {
			logf(log, "%s exists; keeping it", s.name)
		}
	}
	return p.ensureLangfuseBucket(ctx, log)
}

func (p *Provider) ensureLangfuseBucket(ctx context.Context, log io.Writer) error {
	created, err := p.buckets.Ensure(ctx, LangfuseBucket)
	if err != nil {
		return fmt.Errorf("S3 bucket %s in %s: %w", LangfuseBucket, MotoContainer, err)
	}
	if created {
		logf(log, "created S3 bucket %s in %s", LangfuseBucket, MotoContainer)
	}
	return nil
}

type seededSecret struct {
	name string
	gen  func() (map[string]string, error)
}

// langfuseSecrets are Langfuse's four secrets and how each is made.
func langfuseSecrets() []seededSecret {
	return []seededSecret{
		{LangfuseAppSecret, func() (map[string]string, error) {
			return randomFields(map[string]int{"salt": 32, "encryption-key": 32, "nextauth-secret": 32, "admin-password": 12},
				map[string]string{"public-key": "pk-lf-", "secret-key": "sk-lf-"})
		}},
		{LangfusePostgresSecret, func() (map[string]string, error) {
			// One password for both logins, as the chart itself generates it.
			m, err := randomFields(map[string]int{"POSTGRES_PASSWORD": 20}, nil)
			if err != nil {
				return nil, err
			}
			m["POSTGRES_USER"], m["POSTGRES_DB"], m["USERDB_USER"], m["USERDB_PASSWORD"] = "postgres", "langfuse", "langfuse", m["POSTGRES_PASSWORD"]
			return m, nil
		}},
		{LangfuseRedisSecret, func() (map[string]string, error) { return randomFields(map[string]int{"default": 20}, nil) }},
		{LangfuseClickHouseSecret, func() (map[string]string, error) { return randomFields(map[string]int{"password": 20}, nil) }},
	}
}

// randomFields makes hex values of n random bytes for each of hexes (the
// encryption key must be 64 hex characters: 32 bytes), and prefix plus 16
// random bytes for each of prefixed (Langfuse's API key format).
func randomFields(hexes map[string]int, prefixed map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for k, n := range hexes {
		b := make([]byte, n)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		out[k] = hex.EncodeToString(b)
	}
	for k, prefix := range prefixed {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		out[k] = prefix + hex.EncodeToString(b)
	}
	return out, nil
}

// SeedJSON creates secret name as the JSON object gen returns, unless it
// exists. It returns whether it created it.
func SeedJSON(ctx context.Context, s Secrets, name string, gen func() (map[string]string, error)) (bool, error) {
	if _, ok, err := s.Get(ctx, name); err != nil || ok {
		return false, err
	}
	m, err := gen()
	if err != nil {
		return false, err
	}
	v, err := json.Marshal(m)
	if err != nil {
		return false, err
	}
	return true, s.Put(ctx, name, string(v))
}

// Buckets is the part of S3 the provider uses.
type Buckets interface {
	// Ensure creates the bucket unless it exists; created says whether it did.
	Ensure(ctx context.Context, name string) (created bool, err error)
}

type s3Buckets struct{ c *s3.Client }

// NewMotoBuckets is an S3 client for Moto at endpoint.
func NewMotoBuckets(endpoint string) Buckets {
	return s3Buckets{s3.New(s3.Options{
		Region:       MotoRegion,
		BaseEndpoint: aws.String(endpoint),
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider(MotoAccessKey, MotoSecretKey, ""),
	})}
}

func (b s3Buckets) Ensure(ctx context.Context, name string) (bool, error) {
	_, err := b.c.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(name)})
	if err == nil {
		return false, nil
	}
	var nf *s3types.NotFound
	if !errors.As(err, &nf) {
		return false, err
	}
	_, err = b.c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(name),
		CreateBucketConfiguration: &s3types.CreateBucketConfiguration{LocationConstraint: s3types.BucketLocationConstraint(MotoRegion)}})
	var owned *s3types.BucketAlreadyOwnedByYou
	if errors.As(err, &owned) {
		return false, nil
	}
	return err == nil, err
}
