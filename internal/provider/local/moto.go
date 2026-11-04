package local

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// Moto emulates AWS APIs for local mode (Secrets Manager today). It runs as
// a rootless Podman container on the kind network, so ESO reaches it by
// name, and publishes its port on 127.0.0.1 only, for the aws CLI on the
// host. It accepts any credentials; the lab uses dummy ones. It keeps no
// state: lab up re-seeds it from the Secrets ESO keeps (Reseed).
const (
	// MotoImage is Moto 5.2.5, pinned by digest.
	MotoImage     = "docker.io/motoserver/moto:5.2.5@sha256:59a1965d845249b04def37a0e2565f9cc6c44543cb89b68e90f28f344b3b0d1a"
	MotoContainer = "lab-moto"
	motoPort      = 5000 // inside the container
	// MotoHostPort is Moto on 127.0.0.1 of the host.
	MotoHostPort = 4566
	// MotoEndpointHost is Moto from the host: AWS_ENDPOINT_URL_SECRETS_MANAGER.
	MotoEndpointHost = "http://127.0.0.1:4566"
	// MotoEndpointCluster is Moto from the cluster, by name on the kind network.
	MotoEndpointCluster = "http://lab-moto:5000"
	// MotoRegion is the region the lab's secrets live in, as in cloud mode.
	MotoRegion = "ap-south-1"
	// MotoAccessKey and MotoSecretKey are dummy credentials: Moto checks none.
	MotoAccessKey = "lab-dummy"
	MotoSecretKey = "lab-dummy-not-a-secret"
	// SecretStore is the ClusterSecretStore that points ESO at Secrets
	// Manager (Moto here, AWS in cloud mode).
	SecretStore = "lab-secrets"
)

// MotoEnv is what the aws CLI needs to reach Moto from the host. In cloud
// mode the same commands run without it.
func MotoEnv() []string {
	return []string{"AWS_ENDPOINT_URL_SECRETS_MANAGER=" + MotoEndpointHost, "AWS_REGION=" + MotoRegion,
		"AWS_ACCESS_KEY_ID=" + MotoAccessKey, "AWS_SECRET_ACCESS_KEY=" + MotoSecretKey}
}

// ensureMoto starts Moto unless it runs, and waits until it answers.
func (p *Provider) ensureMoto(ctx context.Context, log io.Writer) error {
	if err := p.ensureNetwork(ctx, log); err != nil {
		return err
	}
	info, err := p.rt.Container(ctx, MotoContainer)
	if err != nil {
		return err
	}
	if !info.Exists {
		if err := p.rt.PullImage(ctx, MotoImage); err != nil {
			return err
		}
		logf(log, "creating container %s (%s)", MotoContainer, MotoImage)
		if err := p.rt.CreateContainer(ctx, ContainerSpec{
			Name: MotoContainer, Image: MotoImage,
			Labels:  map[string]string{"lab": "true", "lab.stack": "secrets"},
			Ports:   []Port{{HostIP: "127.0.0.1", HostPort: MotoHostPort, ContainerPort: motoPort}},
			Network: KindNetwork, Aliases: []string{MotoContainer},
		}); err != nil {
			return err
		}
	}
	if !info.Running {
		logf(log, "starting container %s", MotoContainer)
		if err := p.rt.StartContainer(ctx, MotoContainer); err != nil {
			return err
		}
	}
	return p.motoReady(ctx, log)
}

// destroyMoto removes the container. Moto has no volume: nothing else stays.
func (p *Provider) destroyMoto(ctx context.Context, log io.Writer) error {
	logf(log, "removing container %s", MotoContainer)
	return p.rt.RemoveContainer(ctx, MotoContainer)
}

// Secrets is the part of Secrets Manager the provider uses.
type Secrets interface {
	// Get returns the secret's current value; ok is false when there is none.
	Get(ctx context.Context, name string) (value string, ok bool, err error)
	// Put creates the secret, or stores a new current version of it.
	Put(ctx context.Context, name, value string) error
	// Count is how many secrets there are.
	Count(ctx context.Context) (int, error)
}

// smSecrets is Secrets on the AWS SDK, against an endpoint (Moto).
type smSecrets struct{ c *secretsmanager.Client }

// NewMotoSecrets is a Secrets Manager client for Moto at endpoint.
func NewMotoSecrets(endpoint string) Secrets {
	return smSecrets{secretsmanager.New(secretsmanager.Options{
		Region:       MotoRegion,
		BaseEndpoint: aws.String(endpoint),
		Credentials:  credentials.NewStaticCredentialsProvider(MotoAccessKey, MotoSecretKey, ""),
	})}
}

func (s smSecrets) Get(ctx context.Context, name string) (string, bool, error) {
	out, err := s.c.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: aws.String(name)})
	var nf *smtypes.ResourceNotFoundException
	switch {
	case errors.As(err, &nf):
		return "", false, nil
	case err != nil:
		return "", false, err
	}
	return aws.ToString(out.SecretString), true, nil
}

func (s smSecrets) Put(ctx context.Context, name, value string) error {
	_, err := s.c.PutSecretValue(ctx, &secretsmanager.PutSecretValueInput{SecretId: aws.String(name), SecretString: aws.String(value)})
	var nf *smtypes.ResourceNotFoundException
	if errors.As(err, &nf) {
		_, err = s.c.CreateSecret(ctx, &secretsmanager.CreateSecretInput{Name: aws.String(name), SecretString: aws.String(value)})
	}
	return err
}

func (s smSecrets) Count(ctx context.Context) (int, error) {
	n := 0
	pager := secretsmanager.NewListSecretsPaginator(s.c, &secretsmanager.ListSecretsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return 0, err
		}
		n += len(page.SecretList)
	}
	return n, nil
}

// SeedCredentials creates secret name with a user and a random password,
// {"username": user, "password": ...}, unless it exists. It returns whether
// it created it.
func SeedCredentials(ctx context.Context, s Secrets, name, user string) (bool, error) {
	if _, ok, err := s.Get(ctx, name); err != nil || ok {
		return false, err
	}
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return false, err
	}
	v, err := json.Marshal(map[string]string{"username": user, "password": hex.EncodeToString(b)})
	if err != nil {
		return false, err
	}
	return true, s.Put(ctx, name, string(v))
}

// ExternalSecretGVR is ESO's ExternalSecret.
var ExternalSecretGVR = schema.GroupVersionResource{Group: "external-secrets.io", Version: "v1", Resource: "externalsecrets"}

// Reseed puts back into Moto every secret an ExternalSecret on the lab's
// store reads, from the Kubernetes Secret ESO made of it, when Moto lacks
// it. Moto keeps no state, so after a restart it is empty while the
// Secrets (and the database passwords they hold) still exist. A secret
// Moto has is left alone: it is the current value. It returns the names it
// put back, and the ExternalSecrets it skipped (namespace/name): one with
// spec.target.template builds its Secret from the remote value, so the
// Secret is not that value and cannot be copied back.
func Reseed(ctx context.Context, s Secrets, dyn dynamic.Interface, core kubernetes.Interface) (put, skipped []string, err error) {
	list, err := dyn.Resource(ExternalSecretGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("list ExternalSecrets: %w", err)
	}
	want := map[string]map[string]string{} // remote key -> property -> value
	whole := map[string]bool{}             // remote keys read whole (dataFrom.extract)
	for _, es := range list.Items {
		store, _, _ := unstructuredString(es.Object, "spec", "secretStoreRef", "name")
		kind, _, _ := unstructuredString(es.Object, "spec", "secretStoreRef", "kind")
		if store != SecretStore || kind != "ClusterSecretStore" {
			continue
		}
		if _, templated := lookup(es.Object, "spec", "target", "template"); templated {
			skipped = append(skipped, es.GetNamespace()+"/"+es.GetName())
			continue
		}
		target, _, _ := unstructuredString(es.Object, "spec", "target", "name")
		if target == "" {
			target = es.GetName()
		}
		sec, err := core.CoreV1().Secrets(es.GetNamespace()).Get(ctx, target, metav1.GetOptions{})
		if err != nil {
			continue // not synced yet: nothing to put back
		}
		addFromSecret(es.Object, sec, want, whole)
	}
	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if _, ok, err := s.Get(ctx, key); err != nil {
			return put, skipped, err
		} else if ok {
			continue
		}
		v, err := json.Marshal(want[key])
		if err != nil {
			return put, skipped, err
		}
		if err := s.Put(ctx, key, string(v)); err != nil {
			return put, skipped, fmt.Errorf("put %s: %w", key, err)
		}
		put = append(put, key)
	}
	return put, skipped, nil
}

// addFromSecret maps an ExternalSecret's data back onto its remote keys:
// spec.data[] (remoteRef key and property -> secretKey) and
// spec.dataFrom[].extract (the whole JSON object -> every key).
func addFromSecret(es map[string]any, sec *corev1.Secret, want map[string]map[string]string, whole map[string]bool) {
	set := func(key, prop, v string) {
		if want[key] == nil {
			want[key] = map[string]string{}
		}
		want[key][prop] = v
	}
	data, _, _ := unstructuredSlice(es, "spec", "data")
	for _, d := range data {
		m, _ := d.(map[string]any)
		secretKey, _ := m["secretKey"].(string)
		ref, _ := m["remoteRef"].(map[string]any)
		key, _ := ref["key"].(string)
		prop, _ := ref["property"].(string)
		if key == "" || prop == "" || sec.Data[secretKey] == nil {
			continue
		}
		set(key, prop, string(sec.Data[secretKey]))
	}
	from, _, _ := unstructuredSlice(es, "spec", "dataFrom")
	for _, d := range from {
		m, _ := d.(map[string]any)
		ex, _ := m["extract"].(map[string]any)
		key, _ := ex["key"].(string)
		if key == "" {
			continue
		}
		whole[key] = true
		for k, v := range sec.Data {
			set(key, k, string(v))
		}
	}
}

func unstructuredString(obj map[string]any, fields ...string) (string, bool, error) {
	v, ok := lookup(obj, fields...)
	s, _ := v.(string)
	return s, ok, nil
}

func unstructuredSlice(obj map[string]any, fields ...string) ([]any, bool, error) {
	v, ok := lookup(obj, fields...)
	s, _ := v.([]any)
	return s, ok, nil
}

func lookup(obj map[string]any, fields ...string) (any, bool) {
	var cur any = obj
	for _, f := range fields {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[f]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// motoReady waits until Moto answers on its host port.
func (p *Provider) motoReady(ctx context.Context, log io.Writer) error {
	logf(log, "waiting for %s (Secrets Manager on %s)", MotoContainer, MotoEndpointHost)
	return motoAnswers(ctx, p.secrets, time.Minute)
}

// reseed puts the lab's secrets back into Moto from the cluster's Secrets
// (Reseed). Without a cluster to read there is nothing to put back.
func (p *Provider) reseed(ctx context.Context, log io.Writer) error {
	c, err := p.kube()
	if err != nil {
		return err
	}
	if err := p.motoReady(ctx, log); err != nil {
		return err
	}
	put, skipped, err := Reseed(ctx, p.secrets, c.Dynamic, c.Core)
	for _, es := range skipped {
		logf(log, "warning: not putting back the secret ExternalSecret %s reads: it has spec.target.template, so its Secret "+
			"is not the value in Secrets Manager; if %s lost it, put it back by hand", es, MotoContainer)
	}
	for _, k := range put {
		logf(log, "put %s back into %s from its Secret in the cluster (Moto keeps no state across restarts)", k, MotoContainer)
	}
	return err
}

// motoAnswers waits until Moto's Secrets Manager answers on the host port.
func motoAnswers(ctx context.Context, s Secrets, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var last error
	for {
		if _, last = s.Count(ctx); last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("moto does not answer on %s: %w", MotoEndpointHost, last)
		case <-time.After(500 * time.Millisecond):
		}
	}
}
