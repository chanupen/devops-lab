package doctor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/chanupen/devops-lab/internal/compat"
	"github.com/chanupen/devops-lab/internal/engine"
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
		return withRemediation(fail("sudo dnf install podman", "podman not found"),
			repair("sudo dnf install podman", "Install rootless Podman after host approval.", "host-change", true))
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
		return withRemediation(warn("upgrade podman to 5.x or later", "podman %s, rootless, cgroup v2", ver),
			repair("sudo dnf upgrade podman", "Upgrade Podman, then recheck its version.", "host-change", true))
	}
	return ok("%s, rootless, cgroup v2", ver)
}

func checkPodmanSocket(ctx context.Context, sys System, _ Options) Result {
	out, _ := sys.Run(ctx, "systemctl", "--user", "is-active", "podman.socket")
	if out != "active" {
		return withRemediation(fail("systemctl --user enable --now podman.socket", "podman.socket is %q", out),
			repair("systemctl --user enable --now podman.socket", "Enable the rootless user socket.", "host-change", false))
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
		return withRemediation(fail(fmt.Sprintf("sysctl fs.inotify.max_user_watches=%d fs.inotify.max_user_instances=%d (persist in /etc/sysctl.d)", minWatches, minInstances),
			"%s, need ≥%d/≥%d", detail, minWatches, minInstances),
			repair(fmt.Sprintf("sudo sysctl -w fs.inotify.max_user_watches=%d fs.inotify.max_user_instances=%d", max(watches, minWatches), max(instances, minInstances)),
				"Raise runtime limits without lowering existing values; persistent host configuration needs separate review.", "host-change", true))
	}
	return ok("%s", detail)
}

func checkIPTables(_ context.Context, sys System, _ Options) Result {
	builtin := builtinModules(sys)
	var missing []string
	for _, m := range []string{"ip_tables", "iptable_nat", "ip6_tables", "ip6table_nat"} {
		// /sys/module lists loaded modules, but built-in ones only if they have parameters.
		if !sys.Exists("/sys/module/"+m) && !builtin[m] {
			missing = append(missing, m)
		}
	}
	if len(missing) > 0 {
		commands := make([]string, len(missing))
		for i, module := range missing {
			commands[i] = "sudo modprobe " + module
		}
		return withRemediation(fail("list them in /etc/modules-load.d/iptables.conf and modprobe each",
			"not loaded: %s", strings.Join(missing, " ")),
			repair(strings.Join(commands, " && "), "Load missing modules; review persistent modules-load configuration separately.", "host-change", true))
	}
	return ok("ip_tables iptable_nat ip6_tables ip6table_nat")
}

// builtinModules reads /lib/modules/<release>/modules.builtin. Names use
// underscores, as in /sys/module. Errors yield an empty set.
func builtinModules(sys System) map[string]bool {
	set := map[string]bool{}
	rel, err := sys.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return set
	}
	b, err := sys.ReadFile("/lib/modules/" + strings.TrimSpace(string(rel)) + "/modules.builtin")
	if err != nil {
		return set
	}
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		name := l[strings.LastIndex(l, "/")+1:]
		name = strings.TrimSuffix(name, ".ko")
		set[strings.ReplaceAll(name, "-", "_")] = true
	}
	return set
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
		return withRemediation(fail("sudo setsebool -P container_use_devices on", "%s, container_use_devices off", mode),
			repair("sudo setsebool -P container_use_devices on", "Allow container device access without disabling SELinux.", "host-change", true))
	}
	// The host runs enforcing; warn so a drift back to permissive is noticed.
	if mode != "enforcing" {
		return withRemediation(warn("sudo setenforce 1 (and SELINUX=enforcing in /etc/selinux/config)", "%s, container_use_devices on", mode),
			repair("sudo setenforce 1", "Restore runtime enforcement; review persistent SELINUX=enforcing separately.", "host-change", true))
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

// checkVRAM counts memory held by Ollama as available: a loaded model is the
// lab working, not a competing GPU user.
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
	apps, err := sys.Run(ctx, "nvidia-smi", "--query-compute-apps=pid,process_name,used_memory", "--format=csv,noheader,nounits")
	if err != nil {
		return fail("", "nvidia-smi compute apps: %v", err)
	}
	held, others := ollamaVRAM(apps)
	avail := free + held
	detail := fmt.Sprintf("%d MiB free of %d MiB", free, total)
	if held > 0 {
		detail = fmt.Sprintf("%d MiB available of %d MiB (%d free, %d held by ollama)", avail, total, free, held)
	}
	if avail < minVRAMMiB {
		fix := "stop other GPU users"
		if len(others) > 0 {
			fix = "stop other GPU users: " + strings.Join(others, ", ")
		}
		return fail(fix, "%s, models need about %d MiB", detail, minVRAMMiB)
	}
	return ok("%s", detail)
}

// ollamaVRAM sums MiB used by Ollama processes (the server and its runners,
// e.g. /usr/lib/ollama/llama-server) and names the other GPU processes.
func ollamaVRAM(apps string) (held int, others []string) {
	for _, l := range strings.Split(apps, "\n") {
		f := strings.Split(l, ",")
		if len(f) != 3 {
			continue
		}
		name := strings.TrimSpace(f[1])
		mib, err := strconv.Atoi(strings.TrimSpace(f[2]))
		if err != nil {
			continue
		}
		if strings.Contains(strings.ToLower(name), "ollama") {
			held += mib
		} else {
			others = append(others, fmt.Sprintf("%s (pid %s, %d MiB)", name, strings.TrimSpace(f[0]), mib))
		}
	}
	return held, others
}

// aiCheck wraps a check the ai stack needs. Only the full profile asks for
// that stack, so other profiles see a failure as a warning unless the ai
// stack is deployed.
func aiCheck(run func(context.Context, System, Options) Result) func(context.Context, System, Options) Result {
	return func(ctx context.Context, sys System, opts Options) Result {
		r := run(ctx, sys, opts)
		required := slices.Contains(opts.Stacks, "ai") || slices.Contains(opts.RequestedStacks, "ai") ||
			(opts.RequestedStacks == nil && (opts.Profile == "full" || slices.Contains(opts.ExtraStacks, "ai")))
		if r.Status == Fail && !required {
			r.Status = Warn
			r.Detail += " (only the full profile or the ai stack needs it)"
		}
		return r
	}
}

func checkCDI(ctx context.Context, sys System, _ Options) Result {
	out, err := sys.Run(ctx, "nvidia-ctk", "cdi", "list")
	if err != nil {
		return withRemediation(fail("sudo nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml", "nvidia-ctk cdi list: %v", err),
			inspection("nvidia-ctk cdi list", "Install or repair NVIDIA Container Toolkit before generating CDI configuration."))
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.TrimSpace(l) == "nvidia.com/gpu=all" {
			return ok("nvidia.com/gpu=all")
		}
	}
	return withRemediation(fail("sudo nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml", "nvidia.com/gpu=all not listed"),
		repair("sudo nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml", "Generate the NVIDIA CDI specification after host approval.", "host-change", true))
}

func checkOllama(ctx context.Context, sys System, _ Options) Result {
	code, body, err := sys.HTTPGet(ctx, ollamaVersion)
	if err != nil {
		return withRemediation(fail("systemctl --user start ollama", "no answer on :11434: %v", err),
			repair("systemctl --user start ollama", "Start the existing pinned Ollama user service.", "host-change", false))
	}
	if code != 200 {
		return fail("", "HTTP %d from :11434", code)
	}
	digest, err := sys.Run(ctx, "podman", "inspect", "ollama", "--format", "{{.ImageDigest}}")
	if err != nil {
		return fail("systemctl --user status ollama", "cannot read ollama image digest: %v", err)
	}
	digest = strings.TrimSpace(digest)
	if digest != compat.OllamaImageDigest {
		return fail("install docs/ollama.container, then systemctl --user daemon-reload && systemctl --user restart ollama",
			"ollama image %s, need %s", digest, compat.OllamaImageDigest)
	}
	var v struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(body, &v) != nil || v.Version == "" {
		return warn("", "answered on :11434 but version is unreadable")
	}
	return ok("%s on :11434 (%s)", v.Version, digest)
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
	"tofu":      {[]string{"version"}, version{1, 10, 0}, "install OpenTofu ≥ 1.10 from the GitHub releases into ~/.local/bin"},
	"terraform": {[]string{"version"}, version{1, 9, 0}, "install terraform from HashiCorp"},
	"aws":       {[]string{"--version"}, version{2, 0, 0}, "install AWS CLI v2"},
}

func toolCheck(name string) func(context.Context, System, Options) Result {
	return func(ctx context.Context, sys System, _ Options) Result {
		return runTool(ctx, sys, name)
	}
}

func runTool(ctx context.Context, sys System, name string) (r Result) {
	t := tools[name]
	defer func() {
		if len(r.Remediation) == 0 && (r.Status == Fail || r.Status == Warn) {
			r.Remediation = []Remediation{inspection("command -v "+name, "Review PATH and the required tool version. "+t.fix)}
		}
	}()
	if _, err := sys.LookPath(name); err != nil {
		return toolInstall(name, fail(t.fix, "%s not found", name))
	}
	out, err := sys.Run(ctx, name, t.args...)
	if err != nil {
		return fail("", "%s %s: %v", name, strings.Join(t.args, " "), err)
	}
	if name == "go" && strings.Contains(out, "gccgo") {
		return withRemediation(fail("sudo alternatives --set go /usr/lib/golang/bin/go", "gccgo is first on PATH: %s", firstLine(out)),
			repair("sudo alternatives --set go /usr/lib/golang/bin/go", "Select the installed Go compiler after host approval.", "host-change", true))
	}
	v, found := parseVersion(out)
	if !found {
		return warn("", "%s: version not recognised in %q", name, firstLine(out))
	}
	if v.less(t.min) {
		return toolInstall(name, warn(t.fix, "%s %s, want ≥ %s", name, v, t.min))
	}
	return ok("%s %s", name, v)
}

func toolInstall(name string, r Result) Result {
	switch name {
	case "go":
		return withRemediation(r, repair("sudo dnf install golang", "Install or update Go, then verify its version.", "host-change", true))
	case "helm":
		return withRemediation(r, repair("sudo dnf install helm", "Install or update Helm, then verify its version.", "host-change", true))
	default:
		return r
	}
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
	base, known := profileGiB[opts.Profile]
	if !known {
		return fail("use minimal, standard or full", "unknown profile %q", opts.Profile)
	}
	extra := extraStacks(opts)
	need := base
	for _, s := range extra {
		need += extraStackGiB[s]
	}
	mem := opts.Memory
	if mem == nil {
		m, err := ReadMemory(sys)
		if err != nil {
			return fail("", "read /proc/meminfo: %v", err)
		}
		mem = &m
	}
	total, avail := float64(mem.Total)/(1<<30), float64(mem.Available)/(1<<30)
	detail := fmt.Sprintf("%.1f GiB available of %.1f GiB, %s needs ~%s GiB", avail, total, profileName(opts.Profile, extra), gib(need))
	switch {
	case total < need:
		return fail("choose a smaller profile", "%s", detail)
	case avail < need:
		return warn("close memory-heavy apps or choose a smaller profile", "%s", detail)
	}
	return ok("%s", detail)
}

// extraStackGiB is what each stack adds on top of a profile that lacks
// it, measured on the G14 (docs/design.md, "Memory budget"): obs from
// minimal to minimal + obs, policy, secrets (ESO and Moto) and sample-db
// (Postgres and its app) from their own runs, ai by podman stats medians.
// A stack not listed (core, registry: already in every profile) adds 0.
var extraStackGiB = map[string]float64{
	"obs":       0.6,
	"policy":    0.8,
	"secrets":   0.16, // with sample-db: ~0.2
	"sample-db": 0.03,
	"ai":        1.7,
}

// extraStacks are opts.ExtraStacks the profile lacks, without duplicates.
func extraStacks(opts Options) []string {
	in, _ := engine.ProfileStacks(opts.Profile)
	var extra []string
	for _, s := range opts.ExtraStacks {
		if !slices.Contains(in, s) && !slices.Contains(extra, s) {
			extra = append(extra, s)
		}
	}
	return extra
}

// profileName is the profile with its extra stacks, e.g. "minimal + obs",
// as lab plan names it.
func profileName(profile string, extra []string) string {
	if len(extra) == 0 {
		return profile
	}
	return profile + " + " + strings.Join(extra, ", ")
}

// gib prints a threshold with at most one decimal: 4, 4.6, 5.7.
func gib(v float64) string {
	return strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64)
}

// Memory is a validated host memory snapshot in bytes. Swap is not capacity.
type Memory struct {
	Total     uint64
	Available uint64
}

// ProfileMemoryBytes shares doctor's thresholds with guided profile selection.
func ProfileMemoryBytes(profile string) (uint64, error) {
	need, ok := profileGiB[profile]
	if !ok {
		return 0, fmt.Errorf("unknown profile %q", profile)
	}
	return uint64(need * (1 << 30)), nil
}

// ReadMemory reads once; callers may pass the snapshot to subsequent checks.
func ReadMemory(sys System) (Memory, error) {
	b, err := sys.ReadFile("/proc/meminfo")
	if err != nil {
		return Memory{}, err
	}
	values := parseMeminfo(b)
	for _, key := range []string{"MemTotal", "MemAvailable"} {
		if values[key] <= 0 || uint64(values[key]) > ^uint64(0)/1024 {
			return Memory{}, fmt.Errorf("missing or invalid %s", key)
		}
	}
	m := Memory{Total: uint64(values["MemTotal"]) * 1024, Available: uint64(values["MemAvailable"]) * 1024}
	if m.Available > m.Total {
		return Memory{}, fmt.Errorf("MemAvailable exceeds MemTotal")
	}
	return m, nil
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
		if len(f) != 2 || f[1] != "kB" {
			continue
		}
		if n, err := strconv.ParseInt(f[0], 10, 64); err == nil {
			m[k] = n
		}
	}
	return m
}

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
