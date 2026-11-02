package aws

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chanupen/devops-lab/internal/reaper"
)

const fakeAWS = `#!/usr/bin/env bash
echo "aws $*" >>"$CALLS"
arg() { local want=$1; shift; while [ $# -gt 0 ]; do [ "$1" = "$want" ] && { echo "$2"; return; }; shift; done; }
case "$1 $2" in
"s3 cp")
  key=${3#s3://*/}
  [ -f "$FAKE_S3/$key" ] || exit 1
  if [ "$4" = - ]; then cat "$FAKE_S3/$key"; else cp "$FAKE_S3/$key" "$4"; fi ;;
"s3api head-object")
  key=$(arg --key "$@")
  [ -f "$FAKE_S3/$key" ] || exit 254
  cat "$FAKE_S3/$key.modified" ;;
"resourcegroupstaggingapi get-resources") echo '{"ResourceTagMappingList":[]}' ;;
"lambda invoke") for last; do :; done; echo '{}' >"$last"; echo '{}' ;;
esac
exit 0
`

const fakeCurl = `#!/usr/bin/env bash
cp "$FAKE_TOFU_ZIP" "$2"
`

const fakeTofu = `#!/usr/bin/env bash
echo "tofu $*" >>"$CALLS"
`

func zipFiles(t *testing.T, files map[string]string, mode os.FileMode) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func write(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

// The teardown build must not destroy a newer lab: on 2 Nov 2026 build
// cd95d461 checked for bundles at 16:34:10Z, 16 and 27 seconds before a
// new lab uploaded them. These run the real teardown.sh with a fake aws,
// curl and tofu.
func TestTeardownScriptLeavesANewerLabAlone(t *testing.T) {
	for _, tool := range []string{"bash", "jq", "unzip", "sha256sum", "date"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("needs %s", tool)
		}
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "..", "infra", "terraform", "aws", "baseline", "teardown.sh"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	tests := []struct {
		name    string
		expires time.Time
		lockAge time.Duration // 0: no lock
		destroy bool
		output  string
	}{
		{name: "bundle expires-at in the future: exits without destroying or sweeping",
			expires: now.Add(2 * time.Hour), output: "is in the future"},
		{name: "lab apply holds the state lock: exits without destroying",
			expires: now.Add(-time.Hour), lockAge: 5 * time.Minute, output: "OperationTypeApply by lab@host): a lab apply is running"},
		{name: "a lock from a lab that died mid-apply does not block",
			expires: now.Add(-3 * time.Hour), lockAge: 2 * time.Hour, destroy: true, output: "cluster: destroyed"},
		{name: "expired, no lock: destroys",
			expires: now.Add(-time.Hour), destroy: true, output: "cluster: destroyed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			bin, tofuBin, s3, work, tmp := filepath.Join(dir, "bin"), filepath.Join(dir, "tofu"), filepath.Join(dir, "s3"), filepath.Join(dir, "work"), filepath.Join(dir, "tmp")
			for _, d := range []string{tofuBin, work, tmp} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			write(t, filepath.Join(bin, "aws"), fakeAWS, 0o755)
			write(t, filepath.Join(bin, "curl"), fakeCurl, 0o755)
			write(t, filepath.Join(bin, "sleep"), "#!/bin/sh\n", 0o755)
			tofuZip := zipFiles(t, map[string]string{"tofu": fakeTofu}, 0o755)
			write(t, filepath.Join(dir, "tofu.zip"), string(tofuZip), 0o644)
			sum := sha256.Sum256(tofuZip)

			b := zipFiles(t, map[string]string{
				"main.tf":             `resource "aws_vpc" "lab" {}`,
				"backend.tf":          "terraform {\n  backend \"s3\" {\n    key = \"" + reaper.ClusterStateKey + "\"\n  }\n}\n",
				reaper.BundleVarsFile: `{"owner":"lab","expires_at":"` + tt.expires.Format(time.RFC3339) + `"}`,
			}, 0o644)
			write(t, filepath.Join(s3, reaper.BundleKey("cluster")), string(b), 0o644)
			if tt.lockAge > 0 {
				lock := filepath.Join(s3, reaper.ClusterStateKey+".tflock")
				write(t, lock, `{"ID":"x","Operation":"OperationTypeApply","Who":"lab@host","Version":"1.13.2"}`, 0o644)
				write(t, lock+".modified", now.Add(-tt.lockAge).Format(time.RFC3339), 0o644)
			}

			calls := filepath.Join(dir, "calls")
			cmd := exec.Command("bash", script)
			cmd.Dir = work
			cmd.Env = []string{
				"PATH=" + bin + ":" + tofuBin + ":/usr/bin:/bin", "HOME=" + dir, "TMPDIR=" + tmp, "TOFU_BIN_DIR=" + tofuBin,
				"CALLS=" + calls, "FAKE_S3=" + s3, "FAKE_TOFU_ZIP=" + filepath.Join(dir, "tofu.zip"),
				"BUCKET=lab-tofu-state-111122223333", "REAPER=lab-ttl-reaper", "TOPIC=arn:aws:sns:ap-south-1:111122223333:lab-teardown",
				"TOFU_VERSION=1.13.2", "TOFU_SHA256=" + hex.EncodeToString(sum[:]), "ATTEMPT=1", "MAX_ATTEMPTS=6",
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("teardown.sh: %v\n%s", err, out)
			}
			got, _ := os.ReadFile(calls)
			destroyed := strings.Contains(string(got), "tofu destroy")
			swept := strings.Contains(string(got), "aws lambda invoke")
			if destroyed != tt.destroy || swept != tt.destroy {
				t.Errorf("destroyed %v, swept %v, want %v\ncalls:\n%s\noutput:\n%s", destroyed, swept, tt.destroy, got, out)
			}
			if !strings.Contains(string(out), tt.output) {
				t.Errorf("output lacks %q:\n%s", tt.output, out)
			}
			if !tt.destroy && !strings.Contains(string(got), `--subject lab teardown skipped (a newer lab owns the state)`) {
				t.Errorf("summary email not marked skipped:\n%s", got)
			}
		})
	}
}

func TestSetBundleExpiry(t *testing.T) {
	b := zipFiles(t, map[string]string{"main.tf": "x", reaper.BundleVarsFile: `{"owner":"lab","expires_at":"2026-11-02T11:03:00Z","cluster_name":"lab"}`}, 0o644)
	exp := time.Date(2026, 11, 2, 19, 0, 0, 0, time.UTC)
	out, err := SetBundleExpiry(b, exp)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := reaper.BundleExpiry(out); err != nil || !got.Equal(exp) {
		t.Errorf("BundleExpiry = %s, %v; want %s", got, err, exp)
	}
	zr, _ := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	f, _ := zr.Open(reaper.BundleVarsFile)
	vars, _ := io.ReadAll(f)
	if !strings.Contains(string(vars), `"cluster_name": "lab"`) || len(zr.File) != 2 {
		t.Errorf("other variables or files lost: %d files, %s", len(zr.File), vars)
	}
	if _, err := SetBundleExpiry(zipFiles(t, map[string]string{"main.tf": "x"}, 0o644), exp); err == nil {
		t.Error("a bundle without its variables file must be an error")
	}
}

// lab up --mode cloud refuses while a teardown build runs, before it
// looks at anything else.
func TestPreflightRefusesWhileTeardownRuns(t *testing.T) {
	p := &Provider{now: func() time.Time { return t0 },
		teardown: func(context.Context) (string, error) { return "lab-teardown:cd95d461-ac12-49b6-875c-140e383b16ee", nil }}
	err := p.PreflightCheck(context.Background(), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "teardown build lab-teardown:cd95d461-ac12-49b6-875c-140e383b16ee is running") {
		t.Errorf("err = %v", err)
	}
}
