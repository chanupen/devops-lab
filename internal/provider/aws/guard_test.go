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
		"Argo CD":                      gitops.ArgoChartVersion,
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
	hb := func(age time.Duration, dry bool) *reaper.Heartbeat {
		return &reaper.Heartbeat{Time: now.Add(-age), DryRun: dry}
	}
	tests := []struct {
		name string
		st   ReaperStatus
		want string // "" for ready
	}{
		{"armed and recent", ReaperStatus{Heartbeat: hb(14*time.Minute, false)}, ""},
		{"exactly 30 minutes is still recent", ReaperStatus{Heartbeat: hb(30*time.Minute, false)}, ""},
		{"dry run", ReaperStatus{DryRun: true, Heartbeat: hb(time.Minute, true)}, "dry run"},
		{"never ran cleanly", ReaperStatus{}, "never finished a clean run"},
		{"switched on, last run still dry", ReaperStatus{Heartbeat: hb(5*time.Minute, true)}, "was a dry run"},
		{"stale", ReaperStatus{Heartbeat: hb(31*time.Minute, false)}, "31m0s ago"},
		{"an earlier lab needs manual action", ReaperStatus{Heartbeat: &reaper.Heartbeat{Time: now, ManualAction: true,
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
	stmt = stmt[:strings.Index(stmt, "\n  }\n")]
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
