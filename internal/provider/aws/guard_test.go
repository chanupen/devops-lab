package aws

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chanupen/devops-lab/internal/reaper"
)

func TestCheckVersion(t *testing.T) {
	standard := []string{"1.35", "1.37", "1.9", "1.36"}
	if got := NewestVersion(standard); got != "1.37" {
		t.Errorf("NewestVersion = %s", got)
	}
	if err := CheckVersion("1.37", standard); err != nil {
		t.Errorf("newest refused: %v", err)
	}
	for _, v := range []string{"1.36", "1.38"} {
		err := CheckVersion(v, standard)
		if err == nil || !strings.Contains(err.Error(), "newest EKS version in standard support is 1.37") {
			t.Errorf("CheckVersion(%s) = %v", v, err)
		}
	}
	if err := CheckVersion("1.37", nil); err == nil {
		t.Error("no standard versions accepted")
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
