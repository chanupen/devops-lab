package aws

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chanupen/devops-lab/internal/compat"
	"github.com/chanupen/devops-lab/internal/gitops"
	"github.com/chanupen/devops-lab/internal/reaper"
)

func TestNewestVersion(t *testing.T) {
	if got := NewestVersion([]string{"1.35", "1.37", "1.9", "1.36"}); got != "1.37" {
		t.Errorf("NewestVersion = %s", got)
	}
}

func TestChooseVersion(t *testing.T) {
	comps := []Component{
		{Name: "A", AppVersion: "1", MinK8s: "1.26", MaxK8s: "1.37"},
		{Name: "B", AppVersion: "2", MinK8s: "1.19"},
		{Name: "C", AppVersion: "3", MinK8s: "1.33", MaxK8s: "1.36"},
	}
	standard := []string{"1.37", "1.35", "1.36"}
	tests := []struct {
		name, pinned, current string
		standard              []string
		want                  string // version, or "" for an error
		wantMsg               string // in the error, the reason or a warning
	}{
		{"newest all support", "", "", standard, "1.36", "A, B, C all support"},
		{"pin in standard", "1.35", "", standard, "1.35", "pinned"},
		{"pin outside a component's range warns", "1.37", "", standard, "1.37", "outside the stated range of C 3 (1.33–1.36)"},
		{"pin on extended support", "1.32", "", standard, "", "not in EKS standard support"},
		{"running cluster keeps its version", "", "1.35", standard, "1.35", "a new cloud lab would get 1.36"},
		{"running cluster on extended support", "", "1.33", standard, "", "has left EKS standard support"},
		{"nothing compatible", "", "", []string{"1.37", "1.38"}, "", "supported by every pinned component"},
		{"no standard versions", "", "", nil, "", "no version in standard support"},
	}
	for _, tt := range tests {
		c, err := ChooseVersion(tt.pinned, tt.current, tt.standard, comps)
		msg := c.Reason + " " + strings.Join(c.Warnings, "; ")
		if err != nil {
			msg = err.Error()
		}
		switch {
		case tt.want == "" && err == nil:
			t.Errorf("%s: got %s, want an error", tt.name, c.Version)
		case tt.want != "" && (err != nil || c.Version != tt.want):
			t.Errorf("%s: got %q, %v; want %s", tt.name, c.Version, err, tt.want)
		case !strings.Contains(msg, tt.wantMsg):
			t.Errorf("%s: %q does not say %q", tt.name, msg, tt.wantMsg)
		}
	}
}

// The table must describe the versions lab actually installs, and with
// today's pins the cloud lab runs 1.36: Argo CD 3.5 is tested up to 1.36.
func TestCompatibilityMatchesPins(t *testing.T) {
	pins := map[string]string{
		"Karpenter":                    KarpenterChart.Version,
		"AWS Load Balancer Controller": LBCChart.Version,
		"AWS EBS CSI driver":           EBSCSIChart.Version,
		"Argo CD":                      gitops.ArgoChartVersion,
		"Kyverno":                      bootstrapPin(t, "policy.yaml", "kyverno"),
		"External Secrets Operator":    bootstrapChartPin(t, "secrets.yaml", "external-secrets"),
	}
	if len(Compatibility) != len(pins) {
		t.Fatalf("Compatibility has %d rows, want one per pinned component (%d)", len(Compatibility), len(pins))
	}
	for _, c := range Compatibility {
		if pins[c.Name] != c.ChartVersion {
			t.Errorf("%s: table says chart %s, code pins %q", c.Name, c.ChartVersion, pins[c.Name])
		}
		if c.MinK8s == "" || c.Source == "" || c.AppVersion == "" {
			t.Errorf("%s: row needs MinK8s, AppVersion and Source", c.Name)
		}
	}
	if got := PreferredVersion([]string{"1.34", "1.35", "1.36", "1.37"}, Compatibility); got != "1.36" {
		t.Errorf("PreferredVersion = %s, want 1.36 with today's pins", got)
	}
}

func TestReaperReady(t *testing.T) {
	now := t0
	pass := &reaper.SelfTest{TeardownState: reaper.SelfTestOK, TeardownProject: reaper.SelfTestOK}
	hb := func(age time.Duration, dry bool) *reaper.Heartbeat {
		return &reaper.Heartbeat{Time: now.Add(-age), DryRun: dry, Clean: true, SelfTest: pass}
	}
	incident := "teardown state: operation error S3: GetObject, https response error StatusCode: 403, api error AccessDenied"
	tests := []struct {
		name string
		st   ReaperStatus
		want string // "" for ready
	}{
		{"armed and recent", ReaperStatus{Heartbeat: hb(14*time.Minute, false)}, ""},
		{"exactly 30 minutes is still recent", ReaperStatus{Heartbeat: hb(30*time.Minute, false)}, ""},
		{"dry run", ReaperStatus{DryRun: true, Heartbeat: hb(time.Minute, true)}, "dry run"},
		{"never ran", ReaperStatus{}, "never written a heartbeat"},
		{"switched on, last run still dry", ReaperStatus{Heartbeat: hb(5*time.Minute, true)}, "was a dry run"},
		{"stale", ReaperStatus{Heartbeat: hb(31*time.Minute, false)}, "31m0s ago"},
		{"recent but not clean (2 Nov: 403 on the teardown state)", ReaperStatus{Heartbeat: &reaper.Heartbeat{Time: now,
			SelfTest: &reaper.SelfTest{TeardownState: "api error AccessDenied", TeardownProject: reaper.SelfTestOK},
			Errors:   []string{incident, "self-test: teardown state: api error AccessDenied"}}}, "was not clean"},
		{"self-test failed, no other errors recorded", ReaperStatus{Heartbeat: &reaper.Heartbeat{Time: now, Clean: true,
			SelfTest: &reaper.SelfTest{TeardownState: reaper.SelfTestOK, TeardownProject: "CodeBuild project lab-teardown not found"}}},
			"teardown project: CodeBuild project lab-teardown not found"},
		{"heartbeat from a reaper without the self-test", ReaperStatus{Heartbeat: &reaper.Heartbeat{Time: now, Clean: true}},
			"predates its self-test"},
		{"an earlier lab needs manual action", ReaperStatus{Heartbeat: &reaper.Heartbeat{Time: now, ManualAction: true, Clean: true, SelfTest: pass,
			Teardown: "manual action needed: 6 teardown builds did not clear the lab"}}, "not torn down after 6 attempts"},
	}
	for _, tt := range tests {
		err := tt.st.Ready(now)
		switch {
		case tt.want == "" && err != nil:
			t.Errorf("%s: %v", tt.name, err)
		case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
			t.Errorf("%s: err = %v, want %q", tt.name, err, tt.want)
		}
	}
}

func TestExtendIsCappedAt8h(t *testing.T) {
	tests := []struct {
		current time.Time
		d       time.Duration
		ok      bool
	}{
		{t0.Add(time.Hour), time.Hour, true},
		{t0.Add(7 * time.Hour), time.Hour, true},
		{t0.Add(7 * time.Hour), 2 * time.Hour, false},
		{t0.Add(-time.Hour), 8 * time.Hour, true}, // lapsed: from now
		{t0, 9 * time.Hour, false},
	}
	for _, tt := range tests {
		home := t.TempDir()
		if err := WriteExpiry(ExpiryFile(home), tt.current); err != nil {
			t.Fatal(err)
		}
		p := &Provider{opts: Options{Home: home}, now: func() time.Time { return t0 }}
		_, _, err := p.Extend(t.Context(), tt.d, io.Discard)
		refused := err != nil && strings.Contains(err.Error(), "at most 8h0m0s ahead")
		if refused == tt.ok {
			t.Errorf("extend %s from %s: err = %v, want ok %v", tt.d, tt.current.Sub(t0), err, tt.ok)
		}
		if refused {
			if got, _ := ReadExpiry(ExpiryFile(home)); !got.Equal(tt.current) {
				t.Errorf("a refused extend changed the expiry to %s", got)
			}
		}
	}
	if got := maxExtend(t0.Add(7*time.Hour), t0); got != time.Hour {
		t.Errorf("maxExtend = %s", got)
	}
}

func TestBundle(t *testing.T) {
	work := t.TempDir()
	for _, f := range []string{"main.tf", "backend.tf", "lab.auto.tfvars.json", "lbc-iam-policy.json", ".terraform.lock.hcl",
		"lab.tfplan", "terraform.tfstate"} {
		if err := os.WriteFile(filepath.Join(work, f), []byte(f), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(work, ".terraform", "providers"), 0o700); err != nil {
		t.Fatal(err)
	}
	b, err := Bundle(work)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	slices.Sort(names)
	// What tofu destroy needs, with lab's variables; never plans or state.
	if want := []string{".terraform.lock.hcl", "backend.tf", "lab.auto.tfvars.json", "lbc-iam-policy.json", "main.tf"}; !slices.Equal(names, want) {
		t.Errorf("bundle = %v, want %v", names, want)
	}
}

func TestNoLoadBalancerByDefault(t *testing.T) {
	v := LBCValues(info, LabTags("me", t0))
	if v["enableServiceMutatorWebhook"] != false {
		t.Error("the controller must not turn every LoadBalancer Service into an NLB")
	}
	if v["defaultTags"].(map[string]any)["component"] != "load-balancer-controller" {
		t.Error("controller resources need their own component, so the reaper deletes them itself")
	}
}

// Cost guardrails in the cluster module: no NAT gateway, no control plane
// logs, no Container Insights, 20 GB gp3 root disks, standard support.
func TestClusterModuleGuardrails(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join(ClusterModuleDir(filepath.Join("..", "..", "..")), "*.tf"))
	var b strings.Builder
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(data)
	}
	src := b.String()
	for _, banned := range []string{"aws_nat_gateway", "amazon-cloudwatch-observability", "aws_cloudwatch_log_group", "aws_lb\""} {
		if strings.Contains(src, banned) {
			t.Errorf("cluster module contains %s", banned)
		}
	}
	for _, want := range []string{"enabled_cluster_log_types = []", `support_type = "STANDARD"`, "volume_size           = 20", `volume_type           = "gp3"`} {
		if !strings.Contains(src, want) {
			t.Errorf("cluster module lacks %s", want)
		}
	}
	nc := EC2NodeClass(info, nil)
	bdm := field(t, nc, "spec", "blockDeviceMappings").([]any)[0].(map[string]any)["ebs"].(map[string]any)
	if bdm["volumeSize"] != "20Gi" || bdm["volumeType"] != "gp3" {
		t.Errorf("Karpenter root disk = %v", bdm)
	}
}

// The dead man's switch must say when it fails: on 2 Nov 2026 the reaper
// failed every run for five hours and nobody heard. Its async runs go to
// the lab-teardown topic on failure, its role may publish there, it may
// list only teardown/ (so a missing state reads as 404, not 403), may
// write only its own state there, and may see the teardown project for
// its self-test.
func TestReaperFailureDestination(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "infra", "terraform", "aws", "baseline", "reaper.tf"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	// upTo returns src from start to the first line that is exactly end.
	upTo := func(start, end string) string {
		i := strings.Index(src, start)
		if i < 0 {
			t.Fatalf("reaper.tf has no %s", start)
		}
		rest := src[i:]
		j := strings.Index(rest, end)
		if j < 0 {
			t.Fatalf("%s has no end", start)
		}
		return rest[:j]
	}
	cfg := upTo(`resource "aws_lambda_function_event_invoke_config" "reaper"`, "\n}\n")
	for _, want := range []string{"aws_lambda_function.reaper.function_name", "on_failure {", "destination = aws_sns_topic.teardown.arn"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("event invoke config lacks %s", want)
		}
	}
	for sid, wants := range map[string][]string{
		"EmailOwner": {`"sns:Publish"`, "aws_sns_topic.teardown.arn"},
		"ListTeardownAndStateKeys": {`"s3:ListBucket"`, `resources = [aws_s3_bucket.state.arn]`, `variable = "s3:prefix"`,
			`values   = ["teardown/", "teardown/*", "` + reaper.ClusterStateKey + `", "` + reaper.RegistryStateKey + `"]`},
		"ReadStackStates":         {`actions = ["s3:GetObject"]`, `/` + reaper.ClusterStateKey + `"`, `/` + reaper.RegistryStateKey + `"`},
		"ReadTeardownPrefix":      {`"s3:GetObject"`, `/teardown/*"]`},
		"TeardownAttempts":        {`"s3:PutObject", "s3:DeleteObject"`, `/` + reaper.TeardownStateKey + `"]`},
		"SelfTestTeardownProject": {`"codebuild:BatchGetProjects"`, "project/${local.teardown_name}"},
	} {
		loc := regexp.MustCompile(`sid\s+= "` + sid + `"`).FindString(src)
		if loc == "" {
			t.Fatalf("reaper.tf has no %s statement", sid)
		}
		stmt := upTo(loc, "\n  }\n")
		for _, want := range wants {
			if !strings.Contains(stmt, want) {
				t.Errorf("%s lacks %s", sid, want)
			}
		}
	}
	// teardown/ also holds teardown.sh and the module bundles, which the
	// teardown build runs with destroy rights: the reaper must not write them.
	if regexp.MustCompile(`"s3:(PutObject|DeleteObject)"[^}]*/teardown/\*`).MatchString(src) {
		t.Error("the reaper may write anything under teardown/")
	}
}

// The teardown build must not reach non-lab resources: no service-wide
// wildcards, and destructive actions only in the lab region on lab=true.
func TestTeardownRoleIsScoped(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "infra", "terraform", "aws", "baseline", "teardown.tf"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if m := regexp.MustCompile(`"[a-z0-9-]+:\*"`).FindAllString(src, -1); len(m) > 0 {
		t.Errorf("teardown role grants whole services: %v", m)
	}
	i := strings.Index(src, `sid = "DestroyLabTaggedInRegion"`)
	if i < 0 {
		t.Fatal("no DestroyLabTaggedInRegion statement")
	}
	stmt := src[i:]
	end := strings.Index(stmt, "\n  }\n")
	if end < 0 {
		t.Fatal("DestroyLabTaggedInRegion statement has no closing brace")
	}
	stmt = stmt[:end]
	for _, want := range []string{`variable = "aws:RequestedRegion"`, `values   = [var.region]`, `variable = "aws:ResourceTag/lab"`, `values   = ["true"]`} {
		if !strings.Contains(stmt, want) {
			t.Errorf("DestroyLabTaggedInRegion lacks %s", want)
		}
	}
	for _, del := range regexp.MustCompile(`"[a-z0-9-]+:(Delete|Terminate|Detach|Disassociate|Revoke|Remove)[A-Za-z]*"`).FindAllStringIndex(src, -1) {
		action := src[del[0]:del[1]]
		inDestroy := del[0] > i && del[0] < i+len(stmt)
		if !inDestroy && !strings.HasPrefix(action, `"iam:`) && !strings.HasPrefix(action, `"s3:`) && action != `"ec2:DeleteTags"` {
			t.Errorf("%s is granted outside the region- and tag-scoped statement", action)
		}
	}
}

// bootstrapPin is the chart version the bootstrap template pins for an app,
// from its (dict "name" "<app>" ... "version" "<v>") entry.
func bootstrapPin(t *testing.T, template, app string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "gitops", "bootstrap", "templates", template))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`dict "name" "` + regexp.QuoteMeta(app) + `" .*"version" "([^"]+)"`).FindSubmatch(b)
	if m == nil {
		t.Fatalf("no version for %s in %s", app, template)
	}
	return string(m[1])
}

// The EKS version follows the active stacks' rows: with gitops, Argo CD
// holds it at 1.36; with only the cluster stack, Karpenter allows 1.37.
func TestChooseVersionByStacks(t *testing.T) {
	standard := []string{"1.35", "1.36", "1.37"}
	tests := []struct {
		stacks []string
		want   string
	}{
		{nil, "1.36"},
		{[]string{"registry", "cluster", "gitops"}, "1.36"},
		{[]string{"registry", "cluster"}, "1.37"},
	}
	for _, tt := range tests {
		c, err := ChooseVersion("", "", standard, compat.ForStacks("cloud", tt.stacks))
		if err != nil || c.Version != tt.want {
			t.Errorf("stacks %v: %s, %v; want %s", tt.stacks, c.Version, err, tt.want)
		}
	}
}

// bootstrapChartPin is the targetRevision under "chart: <chart>" in a
// bootstrap template.
func bootstrapChartPin(t *testing.T, template, chart string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "gitops", "bootstrap", "templates", template))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`chart: ` + regexp.QuoteMeta(chart) + `\s+targetRevision: (\S+)`).FindSubmatch(b)
	if m == nil {
		t.Fatalf("no targetRevision for chart %s in %s", chart, template)
	}
	return string(m[1])
}
