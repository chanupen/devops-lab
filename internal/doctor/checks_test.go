package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// fake is an in-memory host. Commands are keyed by "name arg1 arg2...".
type fake struct {
	files map[string]string
	paths map[string]bool // existing paths
	bins  map[string]bool
	cmds  map[string]string
	errs  map[string]error
	http  func() (int, []byte, error)
}

func (f fake) system() System {
	return System{
		UID: 1000,
		ReadFile: func(p string) ([]byte, error) {
			s, ok := f.files[p]
			if !ok {
				return nil, fs.ErrNotExist
			}
			return []byte(s), nil
		},
		Exists: func(p string) bool { return f.paths[p] },
		LookPath: func(n string) (string, error) {
			if f.bins[n] {
				return "/usr/bin/" + n, nil
			}
			return "", errors.New("not found")
		},
		Run: func(_ context.Context, name string, args ...string) (string, error) {
			key := strings.Join(append([]string{name}, args...), " ")
			if err := f.errs[key]; err != nil {
				return "", err
			}
			return f.cmds[key], nil
		},
		HTTPGet: func(context.Context, string) (int, []byte, error) {
			if f.http == nil {
				return 0, nil, errors.New("connection refused")
			}
			return f.http()
		},
	}
}

func bins(names ...string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

const cgroupPath = "/sys/fs/cgroup/user.slice/user-1000.slice/user@1000.service/cgroup.controllers"

func TestChecks(t *testing.T) {
	podmanCmds := func(ver, info string) map[string]string {
		return map[string]string{
			"podman version --format {{.Client.Version}}":                               ver,
			"podman info --format {{.Host.Security.Rootless}} {{.Host.CgroupsVersion}}": info,
		}
	}
	tests := []struct {
		name  string
		check func(context.Context, System, Options) Result
		host  fake
		opts  Options
		want  Status
		match string // substring expected in Detail
	}{
		{"podman ok", checkPodman, fake{bins: bins("podman"), cmds: podmanCmds("6.1.3", "true v2")}, Options{}, OK, "6.1.3"},
		{"podman rootful", checkPodman, fake{bins: bins("podman"), cmds: podmanCmds("6.1.3", "false v2")}, Options{}, Fail, "not rootless"},
		{"podman cgroup v1", checkPodman, fake{bins: bins("podman"), cmds: podmanCmds("6.1.3", "true v1")}, Options{}, Fail, "cgroup v1"},
		{"podman old", checkPodman, fake{bins: bins("podman"), cmds: podmanCmds("4.9.0", "true v2")}, Options{}, Warn, "4.9.0"},
		{"podman missing", checkPodman, fake{}, Options{}, Fail, "not found"},

		{"socket active", checkPodmanSocket, fake{cmds: map[string]string{"systemctl --user is-active podman.socket": "active"}}, Options{}, OK, "active"},
		{"socket inactive", checkPodmanSocket, fake{cmds: map[string]string{"systemctl --user is-active podman.socket": "inactive"}}, Options{}, Fail, "inactive"},

		{"cgroups all", checkCgroups, fake{files: map[string]string{cgroupPath: "cpuset cpu io memory pids\n"}}, Options{}, OK, ""},
		{"cgroups no cpuset", checkCgroups, fake{files: map[string]string{cgroupPath: "cpu io memory pids\n"}}, Options{}, Fail, "missing cpuset"},
		{"cgroups unreadable", checkCgroups, fake{}, Options{}, Fail, "read"},

		{"inotify at minimum", checkInotify, fake{files: map[string]string{
			"/proc/sys/fs/inotify/max_user_watches":   "524288\n",
			"/proc/sys/fs/inotify/max_user_instances": "512\n",
		}}, Options{}, OK, "watches=524288"},
		{"inotify low instances", checkInotify, fake{files: map[string]string{
			"/proc/sys/fs/inotify/max_user_watches":   "524288\n",
			"/proc/sys/fs/inotify/max_user_instances": "128\n",
		}}, Options{}, Fail, "instances=128"},

		{"iptables loaded", checkIPTables, fake{paths: map[string]bool{
			"/sys/module/ip_tables": true, "/sys/module/iptable_nat": true,
			"/sys/module/ip6_tables": true, "/sys/module/ip6table_nat": true,
		}}, Options{}, OK, ""},
		{"iptables partial", checkIPTables, fake{paths: map[string]bool{"/sys/module/ip_tables": true}}, Options{}, Fail, "iptable_nat ip6_tables ip6table_nat"},

		{"selinux enforcing", checkSELinux, fake{files: map[string]string{
			"/sys/fs/selinux/enforce": "1", "/sys/fs/selinux/booleans/container_use_devices": "1 1",
		}}, Options{}, OK, "enforcing"},
		{"selinux permissive", checkSELinux, fake{files: map[string]string{
			"/sys/fs/selinux/enforce": "0", "/sys/fs/selinux/booleans/container_use_devices": "1 1",
		}}, Options{}, Warn, "permissive"},
		{"selinux boolean off", checkSELinux, fake{files: map[string]string{
			"/sys/fs/selinux/enforce": "1", "/sys/fs/selinux/booleans/container_use_devices": "0 0",
		}}, Options{}, Fail, "off"},

		{"nvidia ok", checkNvidiaDriver, fake{paths: map[string]bool{"/sys/module/nvidia": true}, cmds: map[string]string{
			"nvidia-smi --query-gpu=name,driver_version --format=csv,noheader": "NVIDIA GeForce GTX 1650, 580.95",
		}}, Options{}, OK, "GTX 1650"},
		{"nvidia no module", checkNvidiaDriver, fake{}, Options{}, Fail, "not loaded"},

		{"vram plenty", checkVRAM, fake{cmds: map[string]string{
			"nvidia-smi --query-gpu=memory.total,memory.free --format=csv,noheader,nounits": "4096, 3728",
		}}, Options{}, OK, "3728 MiB free"},
		{"vram low", checkVRAM, fake{cmds: map[string]string{
			"nvidia-smi --query-gpu=memory.total,memory.free --format=csv,noheader,nounits": "4096, 900",
		}}, Options{}, Warn, "900 MiB free"},
		{"vram no smi", checkVRAM, fake{errs: map[string]error{
			"nvidia-smi --query-gpu=memory.total,memory.free --format=csv,noheader,nounits": errors.New("exit 9"),
		}}, Options{}, Skip, ""},

		{"cdi all", checkCDI, fake{cmds: map[string]string{
			"nvidia-ctk cdi list": "level=info msg=\"Found 3 CDI devices\"\nnvidia.com/gpu=0\nnvidia.com/gpu=all",
		}}, Options{}, OK, ""},
		{"cdi no all", checkCDI, fake{cmds: map[string]string{"nvidia-ctk cdi list": "nvidia.com/gpu=0"}}, Options{}, Fail, "not listed"},

		{"ollama up", checkOllama, fake{http: func() (int, []byte, error) { return 200, []byte(`{"version":"0.40.13"}`), nil }}, Options{}, OK, "0.40.13"},
		{"ollama down", checkOllama, fake{}, Options{}, Fail, "refused"},
		{"ollama 500", checkOllama, fake{http: func() (int, []byte, error) { return 500, nil, nil }}, Options{}, Fail, "HTTP 500"},

		{"go ok", toolCheck("go"), fake{bins: bins("go"), cmds: map[string]string{"go version": "go version go1.27.1 linux/amd64"}}, Options{}, OK, "1.27.1"},
		{"go is gccgo", toolCheck("go"), fake{bins: bins("go"), cmds: map[string]string{"go version": "go version go1.18 gccgo (GCC) 16.2.1"}}, Options{}, Fail, "gccgo"},
		{"kubectl skewed", toolCheck("kubectl"), fake{bins: bins("kubectl"), cmds: map[string]string{
			"kubectl version --client": "Client Version: v1.31.3\nKustomize Version: v5.4.2",
		}}, Options{}, Warn, "1.31.3"},
		{"helm missing", toolCheck("helm"), fake{}, Options{}, Fail, "not found"},
		{"aws ok", toolCheck("aws"), fake{bins: bins("aws"), cmds: map[string]string{
			"aws --version": "aws-cli/2.18.6 Python/3.12.6 Linux/7.2.11",
		}}, Options{}, OK, "2.18.6"},

		{"iac terraform only", checkIaC, fake{bins: bins("terraform"), cmds: map[string]string{
			"terraform version": "Terraform v1.9.8\non linux_amd64",
		}}, Options{}, OK, "terraform 1.9.8"},
		{"iac tofu preferred", checkIaC, fake{bins: bins("tofu", "terraform"), cmds: map[string]string{
			"tofu version": "OpenTofu v1.10.0", "terraform version": "Terraform v1.9.8",
		}}, Options{}, OK, "tofu 1.10.0"},
		{"iac neither", checkIaC, fake{}, Options{}, Fail, "tofu not found"},

		{"memory standard ok", checkMemory, fake{files: map[string]string{
			"/proc/meminfo": "MemTotal:       23977632 kB\nMemFree: 1 kB\nMemAvailable:   20359980 kB\n",
		}}, Options{Profile: "standard"}, OK, "19.4 GiB available"},
		{"memory full busy", checkMemory, fake{files: map[string]string{
			"/proc/meminfo": "MemTotal:       23977632 kB\nMemAvailable:   10485760 kB\n",
		}}, Options{Profile: "full"}, Warn, "10.0 GiB available"},
		{"memory too small", checkMemory, fake{files: map[string]string{
			"/proc/meminfo": "MemTotal:       8388608 kB\nMemAvailable:   8000000 kB\n",
		}}, Options{Profile: "standard"}, Fail, "8.0 GiB"},
		{"memory bad profile", checkMemory, fake{}, Options{Profile: "huge"}, Fail, "unknown profile"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.check(context.Background(), tt.host.system(), tt.opts)
			if got.Status != tt.want {
				t.Fatalf("status = %s, want %s (detail %q)", got.Status, tt.want, got.Detail)
			}
			if !strings.Contains(got.Detail, tt.match) {
				t.Errorf("detail = %q, want it to contain %q", got.Detail, tt.match)
			}
		})
	}
}

func TestParseVersion(t *testing.T) {
	tests := []struct {
		in    string
		want  version
		found bool
	}{
		{"kind v0.33.0 go1.26.7 linux/amd64", version{0, 33, 0}, true},
		{"go version go1.27.1-X:nodwarf5 linux/amd64", version{1, 27, 1}, true},
		{"Version              v0.51.0", version{0, 51, 0}, true},
		{"v4.2.2+gb05881c", version{4, 2, 2}, true},
		{"go1.18", version{1, 18, 0}, true},
		{"no digits here", version{}, false},
	}
	for _, tt := range tests {
		got, found := parseVersion(tt.in)
		if got != tt.want || found != tt.found {
			t.Errorf("parseVersion(%q) = %v, %v; want %v, %v", tt.in, got, found, tt.want, tt.found)
		}
	}
}

func TestRunOrderNamesAndTimeout(t *testing.T) {
	checks := []Check{
		{"slow", func(ctx context.Context, _ System, _ Options) Result {
			<-ctx.Done()
			return fail("", "timed out")
		}},
		{"fast", func(context.Context, System, Options) Result { return ok("fine") }},
	}
	start := time.Now()
	res := Run(context.Background(), fake{}.system(), Options{Timeout: 50 * time.Millisecond}, checks)
	if time.Since(start) > 2*time.Second {
		t.Fatal("Run did not honour the per-check timeout")
	}
	if res[0].Name != "slow" || res[1].Name != "fast" {
		t.Fatalf("results out of order: %+v", res)
	}
	if !Failed(res) {
		t.Error("Failed = false, want true")
	}
}

func TestWriteJSON(t *testing.T) {
	var buf bytes.Buffer
	results := []Result{{Name: "a", Status: OK, Detail: "x"}, {Name: "b", Status: Warn, Detail: "y", Fix: "z"}}
	if err := WriteJSON(&buf, "standard", results); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Profile string `json:"profile"`
		OK      bool   `json:"ok"`
		Results []struct {
			Name, Status, Fix string
		} `json:"results"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.Profile != "standard" || got.Results[1].Status != "warn" || got.Results[1].Fix != "z" {
		t.Errorf("unexpected report: %+v", got)
	}
}

func TestWriteTable(t *testing.T) {
	var buf bytes.Buffer
	results := []Result{
		{Name: "podman", Status: OK, Detail: "6.1.3"},
		{Name: "cgroup delegation", Status: Fail, Detail: "missing cpuset", Fix: "delegate it"},
	}
	if err := WriteTable(&buf, lipgloss.NewRenderer(&buf), results); err != nil { // a buffer is not a TTY, so no colour codes
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"● podman", "✕ cgroup delegation", "fix: delegate it", "1 ok, 0 warn, 1 fail"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q:\n%s", want, out)
		}
	}
}
