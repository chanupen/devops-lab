package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestCheckRemediation(t *testing.T) {
	tests := []struct {
		name, check, command string
		host                 fake
		sudo, manual         bool
	}{
		{"missing podman", "podman", "sudo dnf install podman", fake{}, true, false},
		{"broken podman", "podman", "podman info --debug", fake{bins: bins("podman"), errs: map[string]error{"podman version --format {{.Client.Version}}": errors.New("unavailable")}}, false, true},
		{"rootful podman", "podman", "podman info --debug", fake{bins: bins("podman"), cmds: map[string]string{
			"podman info --format {{.Host.Security.Rootless}} {{.Host.CgroupsVersion}}": "false v2",
		}}, false, true},
		{"old podman", "podman", "sudo dnf upgrade podman", fake{bins: bins("podman"), cmds: map[string]string{
			"podman version --format {{.Client.Version}}":                               "4.9.0",
			"podman info --format {{.Host.Security.Rootless}} {{.Host.CgroupsVersion}}": "true v2",
		}}, true, false},
		{"user socket", "podman.socket", "systemctl --user enable --now podman.socket", fake{}, false, false},
		{"unreadable delegation", "cgroup delegation", "systemctl cat user@.service", fake{}, false, true},
		{"missing delegation", "cgroup delegation", "systemctl cat user@.service", fake{files: map[string]string{cgroupPath: "cpu"}}, false, true},
		{"low inotify", "inotify", "sudo sysctl -w fs.inotify.max_user_watches=524288 fs.inotify.max_user_instances=512", fake{files: map[string]string{
			"/proc/sys/fs/inotify/max_user_watches": "8192", "/proc/sys/fs/inotify/max_user_instances": "128",
		}}, true, false},
		{"retain higher limit", "inotify", "sudo sysctl -w fs.inotify.max_user_watches=1048576 fs.inotify.max_user_instances=512", fake{files: map[string]string{
			"/proc/sys/fs/inotify/max_user_watches": "1048576", "/proc/sys/fs/inotify/max_user_instances": "128",
		}}, true, false},
		{"unreadable limits", "inotify", "sysctl fs.inotify.max_user_watches fs.inotify.max_user_instances", fake{}, false, true},
		{"compound modules", "iptables modules", "sudo modprobe iptable_nat && sudo modprobe ip6_tables && sudo modprobe ip6table_nat",
			fake{paths: map[string]bool{"/sys/module/ip_tables": true}}, true, false},
		{"selinux disabled", "selinux", "sestatus", fake{}, false, true},
		{"selinux boolean", "selinux", "sudo setsebool -P container_use_devices on",
			fake{files: map[string]string{"/sys/fs/selinux/enforce": "1"}}, true, false},
		{"selinux permissive", "selinux", "sudo setenforce 1", fake{files: map[string]string{
			"/sys/fs/selinux/enforce": "0", "/sys/fs/selinux/booleans/container_use_devices": "1",
		}}, true, false},
		{"missing driver", "nvidia driver", "nvidia-smi", fake{}, false, true},
		{"invalid vram", "gpu vram", "nvidia-smi", fake{}, false, true},
		{"missing cdi device", "nvidia cdi", "sudo nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml", fake{}, true, false},
		{"unavailable cdi tool", "nvidia cdi", "nvidia-ctk cdi list",
			fake{errs: map[string]error{"nvidia-ctk cdi list": errors.New("missing")}}, false, true},
		{"ollama stopped", "ollama", "systemctl --user start ollama", fake{}, false, false},
		{"missing go", "go", "sudo dnf install golang", fake{}, true, false},
		{"missing helm", "helm", "sudo dnf install helm", fake{}, true, false},
		{"manual kind install", "kind", "command -v kind", fake{}, false, true},
		{"unrecognised kind", "kind", "command -v kind", fake{bins: bins("kind")}, false, true},
		{"manual iac install", "tofu/terraform", "command -v tofu", fake{}, false, true},
		{"manual aws install", "aws", "command -v aws", fake{}, false, true},
		{"unreadable memory", "memory", "free -h", fake{}, false, true},
		{"low memory", "memory", "free -h", fake{files: map[string]string{
			"/proc/meminfo": "MemTotal: 25165824 kB\nMemAvailable: 1024 kB\n",
		}}, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var check Check
			for _, c := range Checks() {
				if c.Name == tt.check {
					check = c
				}
			}
			if check.Run == nil {
				t.Fatal("check missing")
			}
			sys := tt.host.system()
			run := sys.Run
			sys.Run = func(ctx context.Context, name string, args ...string) (string, error) {
				if name == "sudo" || name == "dnf" || name == "modprobe" ||
					(name == "systemctl" && strings.Contains(strings.Join(args, " "), "enable")) {
					t.Fatalf("remediation executed: %s %v", name, args)
				}
				return run(ctx, name, args...)
			}
			opts := Options{Profile: "minimal"}
			results := Run(context.Background(), sys, opts, []Check{check})
			if results[0].Status != Fail && results[0].Status != Warn {
				t.Fatalf("no diagnostic failure: %+v", results)
			}
			commands := FixCommands(results, opts)
			if len(commands) != 1 {
				t.Fatalf("commands=%+v", commands)
			}
			c := commands[0]
			if c.Command != tt.command || c.RequiresSudo != tt.sudo || c.Manual != tt.manual {
				t.Fatalf("command=%+v", c)
			}
			if (c.Effect == "read-only") != tt.manual || c.RequiresApproval == tt.manual {
				t.Fatalf("unsafe labels: %+v", c)
			}
		})
	}
}

func TestFixDispositionAndPaths(t *testing.T) {
	for _, tt := range []struct {
		name, hint string
		status     Status
		want       bool
	}{
		{"allowed", "allowed", Warn, false},
		{"allowed explanation", " ALLOWED: tested range ", Warn, false},
		{"accepted", "accepted", Warn, false},
		{"accepted explanation", " Accepted: reviewed ", Warn, false},
		{"failure never exempt", "allowed: reviewed", Fail, true},
		{"not an exemption", "not accepted", Warn, true},
		{"empty hint", "", Warn, true},
		{"healthy", "", OK, false},
		{"skipped", "", Skip, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := Result{Name: "fixture", Status: tt.status, Fix: tt.hint}
			commands := FixCommands([]Result{r}, Options{})
			if (len(commands) > 0) != tt.want {
				t.Fatalf("commands=%+v", commands)
			}
		})
	}
	opts := Options{Profile: "minimal", ConfigPath: "/tmp/lab's config.yaml", StatePath: "/tmp/custom state.json", Kubeconfig: "/tmp/custom kubeconfig"}
	results := []Result{
		{Name: "lab cluster", Status: Stopped, Fix: "old hint"},
		withRemediation(Result{Name: "gitops", Status: Fail}, kubeInspection("kubectl --namespace argocd get application lab-bootstrap", "Inspect it.")),
	}
	commands := FixCommands(results, opts)
	if commands[0].Command != "lab start --profile minimal --config '/tmp/lab'\\''s config.yaml' --state '/tmp/custom state.json'" {
		t.Fatal(commands[0].Command)
	}
	if commands[1].Command != "kubectl --namespace argocd get application lab-bootstrap --kubeconfig '/tmp/custom kubeconfig'" {
		t.Fatal(commands[1].Command)
	}
	var out bytes.Buffer
	if err := WriteFixes(&out, opts, results, true); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Profile string `json:"profile"`
		OK      bool   `json:"ok"`
		Results []struct {
			Fix string `json:"fix"`
		} `json:"results"`
		Remediations []json.RawMessage `json:"remediations"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || report.OK || report.Profile != "minimal" || len(report.Remediations) != 2 {
		t.Fatalf("report=%+v, err=%v", report, err)
	}
	if report.Results[0].Fix != commands[0].Command || results[0].Fix != "old hint" {
		t.Fatal("restart hint unresolved or input mutated")
	}
}

type failedFixWriter struct{}

func (failedFixWriter) Write([]byte) (int, error) { return 0, errors.New("fixture write failed") }

func TestFixRendering(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		if err := WriteFixes(failedFixWriter{}, Options{}, nil, asJSON); err == nil {
			t.Fatal("write error lost")
		}
	}
	var out bytes.Buffer
	if err := WriteFixes(&out, Options{}, nil, false); err != nil || !strings.Contains(out.String(), "No fix commands needed") {
		t.Fatalf("out=%s err=%v", out.String(), err)
	}
	out.Reset()
	r := withRemediation(Result{Name: "podman", Status: Fail}, repair("sudo dnf install podman", "Install Podman.", "host-change", true))
	if err := WriteFixes(&out, Options{}, []Result{r}, false); err != nil ||
		!strings.Contains(out.String(), "sudo required; host-change; approval required") {
		t.Fatalf("out=%s err=%v", out.String(), err)
	}
	out.Reset()
	if err := WriteJSON(&out, "minimal", []Result{r}); err != nil || strings.Contains(out.String(), "remediation") {
		t.Fatalf("ordinary JSON changed: %s, %v", out.String(), err)
	}
}
