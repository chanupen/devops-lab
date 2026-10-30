package doctor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	minWatches    = 524288
	minInstances  = 512
	minVRAMMiB    = 3686 // about 3.6 GiB, the budget for 1B–3B 4-bit models
	ollamaVersion = "http://127.0.0.1:11434/api/version"
)

// Memory each profile needs, in GiB (upper end of the documented range).
var profileGiB = map[string]float64{
	"minimal":  4,
	"standard": 12,
	"full":     19,
}

func checkPodman(ctx context.Context, sys System, _ Options) Result {
	if _, err := sys.LookPath("podman"); err != nil {
		return fail("sudo dnf install podman", "podman not found")
	}
	ver, err := sys.Run(ctx, "podman", "version", "--format", "{{.Client.Version}}")
	if err != nil {
		return fail("", "podman version: %v", err)
	}
	info, err := sys.Run(ctx, "podman", "info", "--format", "{{.Host.Security.Rootless}} {{.Host.CgroupsVersion}}")
	if err != nil {
		return fail("", "podman info: %v", err)
	}
	f := strings.Fields(info)
	if len(f) != 2 {
		return fail("", "unexpected podman info output %q", info)
	}
	if f[0] != "true" {
		return fail("run podman as your user, not root", "podman %s is not rootless", ver)
	}
	if f[1] != "v2" {
		return fail("boot with cgroup v2 (systemd.unified_cgroup_hierarchy=1)", "podman %s uses cgroup %s", ver, f[1])
	}
	if v, ok := parseVersion(ver); ok && v.less(version{5, 0, 0}) {
		return warn("upgrade podman to 5.x or later", "podman %s, rootless, cgroup v2", ver)
	}
	return ok("%s, rootless, cgroup v2", ver)
}

func checkPodmanSocket(ctx context.Context, sys System, _ Options) Result {
	out, _ := sys.Run(ctx, "systemctl", "--user", "is-active", "podman.socket")
	if out != "active" {
		return fail("systemctl --user enable --now podman.socket", "podman.socket is %q", out)
	}
	return ok("active")
}

func checkCgroups(_ context.Context, sys System, _ Options) Result {
	path := fmt.Sprintf("/sys/fs/cgroup/user.slice/user-%d.slice/user@%d.service/cgroup.controllers", sys.UID, sys.UID)
	b, err := sys.ReadFile(path)
	if err != nil {
		return fail("", "read %s: %v", path, err)
	}
	have := map[string]bool{}
	for _, c := range strings.Fields(string(b)) {
		have[c] = true
	}
	var missing []string
	for _, c := range []string{"cpuset", "cpu", "io", "memory", "pids"} {
		if !have[c] {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 {
		return fail("Delegate=cpu cpuset io memory pids in /etc/systemd/system/user@.service.d/delegate.conf, then re-login",
			"missing %s", strings.Join(missing, " "))
	}
	return ok("cpuset cpu io memory pids")
}

func checkInotify(_ context.Context, sys System, _ Options) Result {
	watches, err1 := readInt(sys, "/proc/sys/fs/inotify/max_user_watches")
	instances, err2 := readInt(sys, "/proc/sys/fs/inotify/max_user_instances")
	if err1 != nil || err2 != nil {
		return fail("", "read inotify limits: %v", firstErr(err1, err2))
	}
	detail := fmt.Sprintf("watches=%d instances=%d", watches, instances)
	if watches < minWatches || instances < minInstances {
		return fail(fmt.Sprintf("sysctl fs.inotify.max_user_watches=%d fs.inotify.max_user_instances=%d (persist in /etc/sysctl.d)", minWatches, minInstances),
			"%s, need ≥%d/≥%d", detail, minWatches, minInstances)
	}
	return ok("%s", detail)
}

func checkIPTables(_ context.Context, sys System, _ Options) Result {
	var missing []string
	for _, m := range []string{"ip_tables", "iptable_nat", "ip6_tables", "ip6table_nat"} {
		// /sys/module covers both loaded and built-in modules.
		if !sys.Exists("/sys/module/" + m) {
			missing = append(missing, m)
		}
	}
	if len(missing) > 0 {
		return fail("list them in /etc/modules-load.d/iptables.conf and modprobe each",
			"not loaded: %s", strings.Join(missing, " "))
	}
	return ok("ip_tables iptable_nat ip6_tables ip6table_nat")
}

func checkSELinux(_ context.Context, sys System, _ Options) Result {
	b, err := sys.ReadFile("/sys/fs/selinux/enforce")
	if err != nil {
		return warn("", "SELinux disabled")
	}
	mode := "permissive"
	if strings.TrimSpace(string(b)) == "1" {
		mode = "enforcing"
	}
	boolean, err := sys.ReadFile("/sys/fs/selinux/booleans/container_use_devices")
	if err != nil || !strings.HasPrefix(strings.TrimSpace(string(boolean)), "1") {
		return fail("sudo setsebool -P container_use_devices on", "%s, container_use_devices off", mode)
	}
	if mode != "enforcing" {
		return warn("sudo setenforce 1 (and SELINUX=enforcing in /etc/selinux/config)", "%s, container_use_devices on", mode)
	}
	return ok("enforcing, container_use_devices on")
}

func checkNvidiaDriver(ctx context.Context, sys System, _ Options) Result {
	if !sys.Exists("/sys/module/nvidia") {
		return fail("install akmod-nvidia and reboot", "nvidia module not loaded")
	}
	out, err := sys.Run(ctx, "nvidia-smi", "--query-gpu=name,driver_version", "--format=csv,noheader")
	if err != nil {
		return fail("", "nvidia-smi: %v", err)
	}
	return ok("%s", firstLine(out))
}

func checkVRAM(ctx context.Context, sys System, _ Options) Result {
	out, err := sys.Run(ctx, "nvidia-smi", "--query-gpu=memory.total,memory.free", "--format=csv,noheader,nounits")
	if err != nil {
		return Result{Status: Skip, Detail: "nvidia-smi unavailable"}
	}
	f := strings.Split(firstLine(out), ",")
	if len(f) != 2 {
		return fail("", "unexpected nvidia-smi output %q", out)
	}
	total, err1 := strconv.Atoi(strings.TrimSpace(f[0]))
	free, err2 := strconv.Atoi(strings.TrimSpace(f[1]))
	if err1 != nil || err2 != nil {
		return fail("", "unexpected nvidia-smi output %q", out)
	}
	detail := fmt.Sprintf("%d MiB free of %d MiB", free, total)
	if free < minVRAMMiB {
		return warn("unload models or stop other GPU users", "%s, models need about %d MiB", detail, minVRAMMiB)
	}
	return ok("%s", detail)
}

func checkCDI(ctx context.Context, sys System, _ Options) Result {
	out, err := sys.Run(ctx, "nvidia-ctk", "cdi", "list")
	if err != nil {
		return fail("sudo nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml", "nvidia-ctk cdi list: %v", err)
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.TrimSpace(l) == "nvidia.com/gpu=all" {
			return ok("nvidia.com/gpu=all")
		}
	}
	return fail("sudo nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml", "nvidia.com/gpu=all not listed")
}

func checkOllama(ctx context.Context, sys System, _ Options) Result {
	code, body, err := sys.HTTPGet(ctx, ollamaVersion)
	if err != nil {
		return fail("systemctl --user start ollama", "no answer on :11434: %v", err)
	}
	if code != 200 {
		return fail("", "HTTP %d from :11434", code)
	}
	var v struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(body, &v) != nil || v.Version == "" {
		return warn("", "answered on :11434 but version is unreadable")
	}
	return ok("%s on :11434", v.Version)
}

// tool describes how to find and judge a CLI dependency.
type tool struct {
	args []string
	min  version
	fix  string
}

var tools = map[string]tool{
	"go":        {[]string{"version"}, version{1, 27, 0}, "sudo dnf install golang"},
	"kind":      {[]string{"version"}, version{0, 30, 0}, "install kind from the GitHub releases"},
	"kubectl":   {[]string{"version", "--client"}, version{1, 36, 0}, "install kubectl within one minor of the kind node image"},
	"helm":      {[]string{"version", "--short"}, version{3, 15, 0}, "sudo dnf install helm"},
	"k9s":       {[]string{"version", "--short"}, version{0, 40, 0}, "install k9s from the GitHub releases"},
	"tofu":      {[]string{"version"}, version{1, 8, 0}, "sudo dnf install opentofu"},
	"terraform": {[]string{"version"}, version{1, 9, 0}, "install terraform from HashiCorp"},
	"aws":       {[]string{"--version"}, version{2, 0, 0}, "install AWS CLI v2"},
}

func toolCheck(name string) func(context.Context, System, Options) Result {
	return func(ctx context.Context, sys System, _ Options) Result {
		return runTool(ctx, sys, name)
	}
}

func runTool(ctx context.Context, sys System, name string) Result {
	t := tools[name]
	if _, err := sys.LookPath(name); err != nil {
		return fail(t.fix, "%s not found", name)
	}
	out, err := sys.Run(ctx, name, t.args...)
	if err != nil {
		return fail("", "%s %s: %v", name, strings.Join(t.args, " "), err)
	}
	if name == "go" && strings.Contains(out, "gccgo") {
		return fail("sudo alternatives --set go /usr/lib/golang/bin/go", "gccgo is first on PATH: %s", firstLine(out))
	}
	v, found := parseVersion(out)
	if !found {
		return warn("", "%s: version not recognised in %q", name, firstLine(out))
	}
	if v.less(t.min) {
		return warn(t.fix, "%s %s, want ≥ %s", name, v, t.min)
	}
	return ok("%s %s", name, v)
}

// checkIaC passes if either OpenTofu or Terraform is usable; tofu is preferred.
func checkIaC(ctx context.Context, sys System, _ Options) Result {
	r := runTool(ctx, sys, "tofu")
	if r.Status == OK {
		return r
	}
	if rt := runTool(ctx, sys, "terraform"); rt.Status != Fail {
		return rt
	}
	return r
}

func checkMemory(_ context.Context, sys System, opts Options) Result {
	need, known := profileGiB[opts.Profile]
	if !known {
		return fail("use minimal, standard or full", "unknown profile %q", opts.Profile)
	}
	b, err := sys.ReadFile("/proc/meminfo")
	if err != nil {
		return fail("", "read /proc/meminfo: %v", err)
	}
	mem := parseMeminfo(b)
	total, avail := kibToGiB(mem["MemTotal"]), kibToGiB(mem["MemAvailable"])
	detail := fmt.Sprintf("%.1f GiB available of %.1f GiB, %s needs ~%.0f GiB", avail, total, opts.Profile, need)
	switch {
	case total < need:
		return fail("choose a smaller profile", "%s", detail)
	case avail < need:
		return warn("close memory-heavy apps or choose a smaller profile", "%s", detail)
	}
	return ok("%s", detail)
}

func parseMeminfo(b []byte) map[string]int64 {
	m := map[string]int64{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, rest, found := strings.Cut(sc.Text(), ":")
		if !found {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			continue
		}
		if n, err := strconv.ParseInt(f[0], 10, 64); err == nil {
			m[k] = n
		}
	}
	return m
}

func kibToGiB(kib int64) float64 { return float64(kib) / (1 << 20) }

type version struct{ major, minor, patch int }

func (v version) String() string { return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch) }

func (v version) less(o version) bool {
	if v.major != o.major {
		return v.major < o.major
	}
	if v.minor != o.minor {
		return v.minor < o.minor
	}
	return v.patch < o.patch
}

var versionRe = regexp.MustCompile(`v?(\d+)\.(\d+)(?:\.(\d+))?`)

// parseVersion returns the first dotted version in s.
func parseVersion(s string) (version, bool) {
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return version{}, false
	}
	var v version
	v.major, _ = strconv.Atoi(m[1])
	v.minor, _ = strconv.Atoi(m[2])
	v.patch, _ = strconv.Atoi(m[3]) // empty → 0
	return v, true
}

func readInt(sys System, path string) (int, error) {
	b, err := sys.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(b)))
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(l)
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
