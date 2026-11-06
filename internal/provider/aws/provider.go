package aws

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	tfjson "github.com/hashicorp/terraform-json"

	"github.com/chanupen/devops-lab/internal/compat"
	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/gitops"
	"github.com/chanupen/devops-lab/internal/reaper"
	"github.com/chanupen/devops-lab/internal/reaper/sweep"
)

// ClusterName is the lab's EKS cluster. It matches the module's default.
const ClusterName = "lab"

// State keys in the baseline's state bucket, all under cluster/.
const (
	ClusterStateKey  = reaper.ClusterStateKey
	RegistryStateKey = reaper.RegistryStateKey
)

// Stacks lists the stacks the cloud provider runs itself.
func Stacks() []string { return []string{"registry", "cluster"} }

// Options configure a Provider.
type Options struct {
	RepoRoot   string        // the repo: modules are under infra/terraform/aws/cluster
	Home       string        // the cloud lab's home, ~/.lab/cloud: expires-at, kubeconfig, helm
	ChartCache string        // Helm chart archives, shared with the local lab: ~/.lab/helm/charts
	TofuHome   string        // where module work dirs go, ~/.lab/tofu
	Tofu       string        // path to the tofu binary
	Kubeconfig string        // written after the cluster apply
	Profile    string        // AWS CLI profile; the kubeconfig's aws eks get-token uses it too
	Owner      string        // owner tag
	TTL        time.Duration // lab.yaml cloud.ttl
	// K8sVersion is lab.yaml cloud.k8sVersion: empty lets ChooseVersion
	// pick. The local pin (k8sVersion) does not apply to EKS.
	K8sVersion string
	Timeout    time.Duration // per wait (nodes ready, Helm releases)
	// Stacks are the lab's active stacks: only their rows of the
	// compatibility table limit the EKS version. Nil applies every row.
	Stacks []string
}

// Provider runs the registry (ECR) and cluster (VPC, EKS, Karpenter, Load
// Balancer Controller) stacks in cloud mode. It implements engine.Runner.
type Provider struct {
	opts    Options
	cfg     aws.Config
	env     map[string]string
	account string
	helm    gitops.Helm
	myIP    func(ctx context.Context) (string, error)
	now     func() time.Time
	// teardown is the ID of a running lab-teardown build, or "".
	teardown func(ctx context.Context) (string, error)
	// version is the cluster's Kubernetes version once ResolveVersion ran;
	// destroys and outputs do not need it.
	version string
	// inCluster is the in-cluster part of the cluster stack (Karpenter, the
	// NodePool, the Load Balancer Controller).
	inCluster inCluster
	// secrets is AWS Secrets Manager (the sample-db stack's credentials).
	secrets Secrets
}

// inCluster installs and removes what lab puts in the cluster itself.
type inCluster interface {
	Install(ctx context.Context, c ClusterInfo, tags map[string]string, log io.Writer) error
	Remove(ctx context.Context, log io.Writer) error
}

// ClusterInfo is what the cluster module's outputs tell the in-cluster step.
type ClusterInfo struct {
	Name, Endpoint, CA, VPCID         string
	KarpenterQueue, KarpenterNodeRole string
}

// New resolves the session (asking for the MFA code at most once) and
// returns the provider.
func New(ctx context.Context, s Session, opts Options) (*Provider, error) {
	cfg, err := s.Config(ctx)
	if err != nil {
		return nil, err
	}
	_, account, err := Identity(ctx, cfg)
	if err != nil {
		return nil, err
	}
	env, err := s.Env(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if opts.Timeout == 0 {
		opts.Timeout = 20 * time.Minute
	}
	p := &Provider{opts: opts, cfg: cfg, env: env, account: account, myIP: PublicIP, now: time.Now,
		teardown: func(ctx context.Context) (string, error) { return RunningTeardown(ctx, cfg) }}
	p.helm = gitops.NewHelm(opts.Kubeconfig, filepath.Join(opts.Home, "helm"), opts.ChartCache)
	p.secrets = NewSecrets(cfg)
	p.inCluster = &clusterAddons{helm: p.helm, kubeconfig: opts.Kubeconfig, cfg: cfg, timeout: opts.Timeout}
	return p, nil
}

// Account is the AWS account the session resolved to.
func (p *Provider) Account() string { return p.account }

// Supports reports whether Apply and Destroy handle the stack.
func (p *Provider) Supports(stack string) bool { return slices.Contains(Stacks(), stack) }

// Apply brings one stack up. tofu plans and applies in one step: lab up
// shows its own plan and asks before any stack runs.
func (p *Provider) Apply(ctx context.Context, stack string, log io.Writer) error {
	exp, err := p.expiresAt(true)
	if err != nil {
		return err
	}
	switch stack {
	case "registry":
		m := p.registryModule(exp, log)
		if err := m.Apply(ctx); err != nil {
			return err
		}
		outs, err := m.Outputs(ctx)
		if err == nil {
			_, _ = fmt.Fprintf(log, "lab registry: %s\n", outs["lab_registry"])
		}
		return nil
	case "cluster":
		return p.applyCluster(ctx, exp, log)
	}
	return fmt.Errorf("%s: not supported by the cloud provider", stack)
}

// Destroy removes one stack. Removing something already gone is not an error.
func (p *Provider) Destroy(ctx context.Context, stack string, log io.Writer) error {
	exp, err := p.expiresAt(false)
	if err != nil {
		return err
	}
	switch stack {
	case "registry":
		if err := p.registryModule(exp, log).Destroy(ctx); err != nil {
			return err
		}
		return p.removeBundle(ctx, "registry")
	case "cluster":
		if err := p.destroyCluster(ctx, exp, log); err != nil {
			return err
		}
		return p.removeBundle(ctx, "cluster")
	}
	return fmt.Errorf("%s: not supported by the cloud provider", stack)
}

// BundleKey is where the teardown build finds a stack's module bundle.
func BundleKey(stack string) string { return reaper.BundleKey(stack) }

// uploadBundle stores the module as it is about to be applied, so the
// teardown build can destroy it with no laptop. It runs before every apply:
// a half-finished apply must be destroyable too.
func (p *Provider) uploadBundle(ctx context.Context, stack, workDir string, log io.Writer) error {
	b, err := Bundle(workDir)
	if err != nil {
		return fmt.Errorf("teardown bundle: %w", err)
	}
	if _, err := s3.NewFromConfig(p.cfg).PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(StateBucket(p.account)), Key: aws.String(BundleKey(stack)), Body: bytes.NewReader(b),
	}); err != nil {
		return fmt.Errorf("upload teardown bundle: %w", err)
	}
	_, _ = fmt.Fprintf(log, "teardown bundle: s3://%s/%s\n", StateBucket(p.account), BundleKey(stack))
	return nil
}

// extendBundle rewrites a stack's teardown bundle with expires_at = exp.
// A stack without a bundle has nothing to extend.
func (p *Provider) extendBundle(ctx context.Context, stack string, exp time.Time, log io.Writer) error {
	client := s3.NewFromConfig(p.cfg)
	bucket, key := aws.String(StateBucket(p.account)), aws.String(BundleKey(stack))
	out, err := client.GetObject(ctx, &s3.GetObjectInput{Bucket: bucket, Key: key})
	var nsk *s3types.NoSuchKey
	if errors.As(err, &nsk) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("teardown bundle %s: %w", BundleKey(stack), err)
	}
	b, err := io.ReadAll(out.Body)
	_ = out.Body.Close()
	if err != nil {
		return fmt.Errorf("teardown bundle %s: %w", BundleKey(stack), err)
	}
	if b, err = SetBundleExpiry(b, exp); err != nil {
		return fmt.Errorf("teardown bundle %s: %w", BundleKey(stack), err)
	}
	if _, err := client.PutObject(ctx, &s3.PutObjectInput{Bucket: bucket, Key: key, Body: bytes.NewReader(b)}); err != nil {
		return fmt.Errorf("teardown bundle %s: %w", BundleKey(stack), err)
	}
	_, _ = fmt.Fprintf(log, "teardown bundle %s: expires-at %s\n", BundleKey(stack), exp.UTC().Format(time.RFC3339))
	return nil
}

func (p *Provider) removeBundle(ctx context.Context, stack string) error {
	_, err := s3.NewFromConfig(p.cfg).DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(StateBucket(p.account)), Key: aws.String(BundleKey(stack))})
	return err
}

func (p *Provider) applyCluster(ctx context.Context, exp time.Time, log io.Writer) error {
	if _, err := p.ResolveVersion(ctx, log); err != nil {
		return err
	}
	ip, err := p.myIP(ctx)
	if err != nil {
		return fmt.Errorf("find this host's public IP for the EKS API allow list: %w", err)
	}
	_, _ = fmt.Fprintf(log, "EKS API endpoint restricted to %s/32\n", ip)
	m := p.clusterModule(exp, []string{ip + "/32"}, log)
	if err := m.Apply(ctx); err != nil {
		return err
	}
	outs, err := m.Outputs(ctx)
	if err != nil {
		return fmt.Errorf("cluster outputs: %w", err)
	}
	c := ClusterInfo{
		Name: outs["cluster_name"], Endpoint: outs["cluster_endpoint"], CA: outs["cluster_ca"], VPCID: outs["vpc_id"],
		KarpenterQueue: outs["karpenter_queue"], KarpenterNodeRole: outs["karpenter_node_role"],
	}
	if c.Name == "" || c.Endpoint == "" {
		return errors.New("cluster outputs are missing cluster_name or cluster_endpoint")
	}
	if err := WriteKubeconfig(p.opts.Kubeconfig, c, p.opts.Profile); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(log, "kubeconfig written to %s (aws eks get-token --profile %s)\n", p.opts.Kubeconfig, p.opts.Profile)
	return p.inCluster.Install(ctx, c, p.tags(exp), log)
}

// destroyCluster removes what lab installed in the cluster first, so
// Karpenter's nodes and the controller's load balancers are gone before
// tofu deletes the subnets they sit in; then it destroys the module.
func (p *Provider) destroyCluster(ctx context.Context, exp time.Time, log io.Writer) error {
	if _, err := os.Stat(p.opts.Kubeconfig); err == nil {
		if err := p.inCluster.Remove(ctx, log); err != nil {
			// The cluster may be half gone already; the sweep below and the
			// tofu destroy still run, and lab down lists anything left.
			_, _ = fmt.Fprintf(log, "warning: in-cluster cleanup: %v\n", err)
		}
	}
	if err := sweepClusterInstances(ctx, p.cfg, ClusterName, log); err != nil {
		_, _ = fmt.Fprintf(log, "warning: terminate Karpenter nodes: %v\n", err)
	}
	// tofu needs a valid CIDR list to plan the destroy; the value is unused.
	if err := p.clusterModule(exp, []string{"127.0.0.1/32"}, log).Destroy(ctx); err != nil {
		return err
	}
	if err := sweepClusterVolumes(ctx, p.cfg, log); err != nil {
		_, _ = fmt.Fprintf(log, "warning: delete EBS volumes: %v\n", err)
	}
	if err := os.Remove(p.opts.Kubeconfig); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (p *Provider) tags(exp time.Time) map[string]string {
	return LabTags(p.opts.Owner, exp)
}

// LabTags are the tags every expiring lab resource carries.
func LabTags(owner string, exp time.Time) map[string]string {
	return map[string]string{"lab": "true", "owner": owner, ExpiresTag: exp.UTC().Format(time.RFC3339)}
}

func (p *Provider) module(name, dir, key string, vars map[string]any, log io.Writer) *Module {
	stack := strings.TrimPrefix(name, "cloud-")
	return &Module{
		CheckPlan:   func(ctx context.Context, plan *tfjson.Plan) error { return checkPlanPolicies(ctx, p.cfg, plan, log) },
		BeforeApply: func(ctx context.Context, workDir string) error { return p.uploadBundle(ctx, stack, workDir, log) },
		ModuleDir:   dir,
		WorkDir:     filepath.Join(p.opts.TofuHome, name),
		Tofu:        p.opts.Tofu,
		Bucket:      StateBucket(p.account),
		Key:         key,
		Vars:        vars,
		Env:         p.env,
		Out:         log,
	}
}

func (p *Provider) registryModule(exp time.Time, log io.Writer) *Module {
	return p.module("cloud-registry", RegistryModuleDir(p.opts.RepoRoot), RegistryStateKey, map[string]any{
		"owner": p.opts.Owner, "expires_at": exp.UTC().Format(time.RFC3339),
	}, log)
}

func (p *Provider) clusterModule(exp time.Time, cidrs []string, log io.Writer) *Module {
	vars := map[string]any{
		"owner": p.opts.Owner, "expires_at": exp.UTC().Format(time.RFC3339),
		"cluster_name": ClusterName, "api_allowed_cidrs": cidrs,
	}
	if p.version != "" {
		vars["k8s_version"] = p.version
	}
	return p.module("cloud-cluster", ClusterModuleDir(p.opts.RepoRoot), ClusterStateKey, vars, log)
}

// ResolveVersion picks the cluster's Kubernetes version (ChooseVersion:
// standard support only, the running cluster's version, else the newest
// every pinned component supports), logs why and remembers it for the
// cluster module.
func (p *Provider) ResolveVersion(ctx context.Context, log io.Writer) (VersionChoice, error) {
	standard, err := StandardVersions(ctx, p.cfg)
	if err != nil {
		return VersionChoice{}, err
	}
	current, err := ClusterVersion(ctx, p.cfg, ClusterName)
	if err != nil {
		return VersionChoice{}, err
	}
	c, err := ChooseVersion(p.opts.K8sVersion, current, standard, compat.ForStacks("cloud", p.opts.Stacks))
	if err != nil {
		return c, err
	}
	p.version = c.Version
	_, _ = fmt.Fprintf(log, "Kubernetes %s on EKS: %s\n", c.Version, c.Reason)
	for _, w := range c.Warnings {
		_, _ = fmt.Fprintf(log, "warning: %s\n", w)
	}
	return c, nil
}

// ClusterModuleDir and RegistryModuleDir locate the modules in the repo.
func ClusterModuleDir(repo string) string {
	return filepath.Join(repo, "infra", "terraform", "aws", "cluster")
}

// RegistryModuleDir is the ECR module, a separate root module.
func RegistryModuleDir(repo string) string { return filepath.Join(ClusterModuleDir(repo), "registry") }

// expiresAt is the lab's expiry. When create is set and there is none yet,
// or it has passed (a fresh lab up after a lapse), it starts a new TTL.
func (p *Provider) expiresAt(create bool) (time.Time, error) {
	path := ExpiryFile(p.opts.Home)
	exp, err := ReadExpiry(path)
	switch {
	case err == nil && (!create || exp.After(p.now())):
		return exp, nil
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return time.Time{}, err
	case !create:
		// Destroying or planning with no record: the expiry a new lab would get.
		return p.now().Add(p.opts.TTL).Truncate(time.Minute), nil
	}
	exp = p.now().Add(p.opts.TTL).Truncate(time.Minute)
	return exp, WriteExpiry(path, exp)
}

// ExpiryFile holds the cloud lab's expires-at, so every apply, lab extend
// and the next tofu plan agree on one value.
func ExpiryFile(home string) string { return filepath.Join(home, "expires-at") }

// ReadExpiry reads an expiry file.
func ReadExpiry(path string) (time.Time, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339, strings.TrimSpace(string(b)))
}

// WriteExpiry writes an expiry file.
func WriteExpiry(path string, t time.Time) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(t.UTC().Format(time.RFC3339)+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ClearExpiry removes the expiry once nothing is left.
func ClearExpiry(home string) error {
	if err := os.Remove(ExpiryFile(home)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Extend moves expires-at forward by d from the later of now and the
// current expiry, on every lab resource at once: tagged AWS resources
// directly, then the EC2NodeClass and the Load Balancer Controller's
// defaultTags, so nodes and load balancers created later get it too. The
// next tofu plan reads the same value from the expiry file.
func (p *Provider) Extend(ctx context.Context, d time.Duration, log io.Writer) (time.Time, []string, error) {
	old, err := ReadExpiry(ExpiryFile(p.opts.Home))
	if err != nil {
		return time.Time{}, nil, fmt.Errorf("no cloud lab to extend (%w): lab up --mode cloud starts one", err)
	}
	exp := NextExpiry(old, p.now(), d)
	if left := exp.Sub(p.now()); left > config.MaxTTL {
		return old, nil, fmt.Errorf("that would put expires-at %s from now; a cloud lab may run at most %s ahead (lab extend %s is the most now)",
			left.Round(time.Minute), config.MaxTTL, maxExtend(old, p.now()))
	}
	if err := WriteExpiry(ExpiryFile(p.opts.Home), exp); err != nil {
		return time.Time{}, nil, err
	}
	// The bundles first: the reaper and the teardown build go by their
	// expires-at, and the tagging API shows new tags only minutes later.
	for _, stack := range Stacks() {
		if err := p.extendBundle(ctx, stack, exp, log); err != nil {
			return exp, nil, err
		}
	}
	_, failed, err := Retag(ctx, p.cfg, exp, log)
	if err != nil {
		return exp, failed, err
	}
	if _, err := os.Stat(p.opts.Kubeconfig); err != nil {
		return exp, failed, nil // no cluster: nothing in Kubernetes to update
	}
	outs, err := p.clusterModule(exp, []string{"127.0.0.1/32"}, log).Outputs(ctx)
	if err != nil {
		return exp, failed, fmt.Errorf("cluster outputs: %w", err)
	}
	c := ClusterInfo{
		Name: outs["cluster_name"], Endpoint: outs["cluster_endpoint"], CA: outs["cluster_ca"], VPCID: outs["vpc_id"],
		KarpenterQueue: outs["karpenter_queue"], KarpenterNodeRole: outs["karpenter_node_role"],
	}
	return exp, failed, p.inCluster.Install(ctx, c, p.tags(exp), log)
}

// Leftovers lists lab=true resources outside the baseline.
func (p *Provider) Leftovers(ctx context.Context) (LeftoverReport, error) {
	return Leftovers(ctx, p.cfg)
}

// Sweep is lab down's safety net after OpenTofu has destroyed the
// environment: it deletes any ENI, EBS volume or Secrets Manager secret
// still tagged with this environment's owner and expires-at (sweep.Run),
// listed both by the tagging API and directly. It needs the recorded
// expiry: without one it sweeps nothing, and the reaper handles what is
// left once it expires.
func (p *Provider) Sweep(ctx context.Context, log io.Writer) (sweep.Result, error) {
	exp, err := ReadExpiry(ExpiryFile(p.opts.Home))
	if errors.Is(err, os.ErrNotExist) {
		_, _ = fmt.Fprintln(log, "sweep: no expires-at recorded for this lab; nothing swept")
		return sweep.Result{}, nil
	}
	if err != nil {
		return sweep.Result{}, err
	}
	a := reaper.NewAWS(p.cfg)
	lister := sweep.Listers{a, sweep.Lister{AWS: a, Account: p.account, Region: Region}}
	env := map[string]string{sweep.OwnerTag: p.opts.Owner, reaper.ExpiresTag: exp.UTC().Format(time.RFC3339)}
	logf := func(format string, args ...any) { _, _ = fmt.Fprintf(log, "sweep: "+format+"\n", args...) }
	return sweep.Run(ctx, lister, a, a, env, sweep.Kinds, logf)
}

// Finish clears the expiry once lab down has destroyed every stack.
func (p *Provider) Finish() error { return ClearExpiry(p.opts.Home) }

// NextExpiry is d after the later of the current expiry and now, so
// extending a lab that has already lapsed still gives it d from now.
func NextExpiry(current, now time.Time, d time.Duration) time.Time {
	if current.Before(now) {
		current = now
	}
	return current.Add(d).Truncate(time.Minute)
}

// ModulePlan is one module's saved plan as tofu shows it.
type ModulePlan struct {
	Stack   string
	Changes bool
	Text    string
}

// Plan runs tofu plan for the registry and cluster modules and returns the
// plans. It changes nothing in AWS and writes no expiry: a lab that does
// not exist yet is planned with expires-at = now + TTL.
func (p *Provider) Plan(ctx context.Context, log io.Writer) ([]ModulePlan, error) {
	exp, err := p.expiresAt(false)
	if err != nil {
		return nil, err
	}
	ip, err := p.myIP(ctx)
	if err != nil {
		return nil, fmt.Errorf("find this host's public IP for the EKS API allow list: %w", err)
	}
	if _, err := p.ResolveVersion(ctx, log); err != nil {
		return nil, err
	}
	var plans []ModulePlan
	for _, s := range Stacks() {
		m := p.registryModule(exp, log)
		if s == "cluster" {
			m = p.clusterModule(exp, []string{ip + "/32"}, log)
		}
		changes, err := m.Plan(ctx)
		if err != nil {
			return plans, fmt.Errorf("%s: %w", s, err)
		}
		text, err := m.ShowPlan(ctx)
		if err != nil {
			return plans, fmt.Errorf("%s: show plan: %w", s, err)
		}
		plans = append(plans, ModulePlan{Stack: s, Changes: changes, Text: text})
	}
	return plans, nil
}

// maxExtend is the longest extension the cap allows from now.
func maxExtend(current, now time.Time) time.Duration {
	if current.Before(now) {
		current = now
	}
	d := now.Add(config.MaxTTL).Sub(current).Truncate(time.Minute)
	if d < 0 {
		return 0
	}
	return d
}

// checkPlanPolicies checks the AWS managed policies a saved plan attaches.
func checkPlanPolicies(ctx context.Context, cfg aws.Config, plan *tfjson.Plan, log io.Writer) error {
	arns := PlanManagedPolicies(plan)
	if len(arns) == 0 {
		return nil
	}
	if err := CheckManagedPolicies(ctx, iam.NewFromConfig(cfg), arns); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(log, "AWS managed policies: %d checked, all exist\n", len(arns))
	return nil
}
