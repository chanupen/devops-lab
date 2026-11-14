# Run the lab locally

[Docs index](../README.md) · [Architecture](../concepts/architecture.md) ·
[Cloud guide](cloud.md)

This guide starts the default `minimal` profile plus observability (`obs`).
It does not start a drill or the optional AI stack.

## Before you start

Use a Linux workload host. Fedora 45 is the tested setup. A Mac can connect
over SSH; it does not run the local provider.

| Requirement | What it is for |
| --- | --- |
| Rootless Podman, preferably 5.x or newer | Runs kind nodes and the registry; never Docker |
| systemd user services and cgroup v2 | Podman socket and resource accounting |
| Delegated `cpuset cpu io memory pids` controllers | Lets rootless kind manage nodes |
| inotify watches at least 524288; instances at least 512 | Kubernetes watches |
| Loaded `ip_tables`, `iptable_nat`, `ip6_tables`, `ip6table_nat` modules | Node networking |
| SELinux `container_use_devices` enabled on an SELinux host | Device access; enforcing is the tested mode |
| Go 1.27.1 or newer, Git and make | Builds the CLI |
| kind 0.30+, kubectl compatible with Kubernetes 1.36, Helm 3.15+, k9s 0.40+ | Tools checked by doctor; k9s is the TUI handoff |
| jq, curl, OpenSSH and base64 | Exercises, access and certificate extraction |
| OpenTofu 1.10+ or Terraform 1.9+, and AWS CLI v2 | Doctor checks these even locally; local mode does not use real AWS |
| A read-only GitOps deploy key | Lets Argo CD read the repository over SSH |

The default deployment measured about 4.6 GiB of container memory. Leave
headroom for the host, image pulls and builds. Doctor checks a 4 GiB memory
threshold for `minimal`, not the complete cost of `extraStacks`.
GPU, NVIDIA CDI and host Ollama are optional unless you select `ai` or `full`.

> **Host safety:** get approval before installing packages with sudo or changing
> kernel modules, cgroup delegation, sysctl or SELinux settings. Doctor prints
> suggested fixes; it does not apply them. Do not blindly run every fix.

## Get the source and install

The repository is public; cloning needs no login.

```sh
git clone https://github.com/chanupen/devops-lab.git
```

**What you will see:** clone progress and a `devops-lab` directory.

```sh
cd devops-lab
```

**What you will see:** no output; the remaining commands run from the checkout.

```sh
git config core.hooksPath .githooks
```

**What you will see:** no output. The clone now uses the repository's push guard.

```sh
export PATH="$HOME/.local/bin:$PATH"
export KIND_EXPERIMENTAL_PROVIDER=podman
```

**What you will see:** no output from either export. This shell finds the
installed binary and selects Podman for kind.

```sh
make install
```

**What you will see:** a Go build, then `installed .../.local/bin/lab, version ...`.
Warnings say if that directory is absent from PATH or an older `lab` takes
precedence. `make install BINDIR=/your/bin` changes the destination and reports
that path. No sudo is needed.

```sh
lab version
```

**What you will see:** the installed build's version. A source build may show a
Git suffix after `v1.1.0`. Re-run installation after updating source.

Enable the user Podman API socket if it is not already enabled:

```sh
systemctl --user enable --now podman.socket
```

**What you will see:** systemd may report an enabled unit. Doctor should then
report `podman.socket` as `active`.

## Configure GitOps

Keep the private deploy key outside Git. If you already have a dedicated,
read-only key for this repository, use it. Otherwise create one:

```sh
mkdir -p "$HOME/.ssh"
chmod 700 "$HOME/.ssh"
```

**What you will see:** no output from either command; the SSH directory exists
and is accessible only to your user.

```sh
ssh-keygen -t ed25519 -f "$HOME/.ssh/devops-lab-deploy" -N '' -C lab-gitops
```

**What you will see:** key-generation output and private/public key paths.
Stop if it offers to overwrite an existing key. This dedicated key has no
passphrase so Argo CD can use it non-interactively; grant it read-only access.
Add only its `.pub` file to this repository's deploy keys, with write access
disabled. Never paste the private key into a document or commit.

Review the existing `lab.yaml`. Keep its other settings and set:

```yaml
mode: local
profile: minimal
extraStacks: [obs]
gitops: argocd
repo:
  url: git@github.com:chanupen/devops-lab.git
  sshKeyFile: ~/.ssh/devops-lab-deploy
```

This is a fragment, not a replacement for the whole file. The source must
contain this repository's `gitops/` tree on `main`. A plain up syncs `main`.
Keep `gitops: argocd`. Flux is planned, not built, and lab refuses `gitops: flux`.

Config discovery checks `./lab.yaml`, then `~/.lab/lab.yaml`. Run from the
checkout. A remote shell can use a symlink from `~/.lab/lab.yaml` to this
checkout's file; do not replace an existing config without reviewing it.

## Read lab doctor

**Read-only; no lab is created.**

```sh
lab doctor --mode local --profile minimal
```

**What you will see:** a row per check, suggested fixes under some rows, and
a summary such as `... ok, ... warn, 0 fail`. Counts depend on the host.

| Symbol/status | Meaning | What to do |
| --- | --- | --- |
| `●` / `ok` | Check passed | Continue |
| `◐` / `warn` | Not recommended, but not a blocking failure | Read the detail; decide whether it affects your selected stacks |
| `✕` / `fail` | Blocking readiness failure; command exits non-zero | Fix it before starting |
| `○` / `skip` | Nothing to inspect or a prerequisite is absent | Check why it was skipped |
| `○` / `stopped` | Existing lab containers are stopped, often after a reboot | Up can restart the main nodes and registry |

Before a first up, missing cluster, registry and deployed-app checks need not
mean a broken installation. Missing Podman, its socket or cgroup delegation
does. NVIDIA/Ollama failures are warnings for this profile when `ai` is not
deployed; they become failures when AI is required.

Pay particular attention to `lan listeners`, `memory`, `ghcr.io download`,
`gitops revision` and `drill`. A LAN listener is unsafe. Low available memory
or a slow image CDN can make the first install fail. An old active drill is
not a clean starting point. Use the reported fix and
[known issues](../../prompts/91-known-issues.md); do not disable checks.
Memory compares both total and available RAM with the profile threshold:
insufficient total fails, while low available memory warns. Do not treat
a zero exit status as proof of enough free memory for every selected stack.

For a machine-readable report:

```sh
lab doctor --mode local --profile minimal --read-only --json
```

**What you will see:** JSON with `profile`, `ok`, and `results`. Each result has
`name`, `status`, `detail`, and sometimes `fix`. Only failures make `ok` false.

## Plan and start

Start with a guided preview. Use an explicit minimal profile for this first
tour so the plan matches the checked-in default.

```sh
lab start --profile minimal --dry-run
```

**What you will see:** doctor results, available RAM, the selected profile,
extra stacks and the dependency-ordered plan. The final line says no apply,
restart, chart download or state write occurred. Failed readiness checks block startup.
The registry check says `write permission untested`; it only observes.

After a reboot, stopped main nodes, Zot or Moto produce proposed restart lines.
API-dependent AI/GitOps health is labelled unverified until restart, not a
startup blocker. With nodes down, kept stacks show recorded state rather than
claiming they are applied and healthy. Host failures such as low inotify limits
still block; requested AI still needs GPU readiness. `lab next` suggests
`lab start --profile <configured>` for stopped containers unless a host failure
or active exercise needs attention first. Ordinary doctor still reports health
failures while the API server is unreachable.

For copyable repair guidance without executing anything:

```sh
lab doctor --fix --profile minimal
```

**What you will see:** print-only commands with sudo/approval labels, or an
explicit manual prerequisite and read-only inspection. Accepted/allowed
warnings do not produce fixes. The registry write probe is not run. Stopped
main-runtime components show `lab start` guidance, not API health errors.
`--fix --json` adds structured remediations to the ordinary report. Review
and approve any host/lab changes before running a printed command yourself.

Without `--profile`, start picks the largest profile that fits available RAM:
4 GiB for minimal, 12 GiB for standard, 19 GiB for full. Swap does not count.
An explicit profile still needs that much available RAM. Selection is temporary;
it never rewrites `lab.yaml` or drops `extraStacks`. Extras need more headroom.

> **Changes the lab:** the next command asks for confirmation before creating
> containers, downloading charts or applying stacks. Review the plan first.
> `--yes` skips the prompt, not readiness checks. Finish active labs/drills
> explicitly; start never restores them automatically.

```sh
lab start --profile minimal
```

**What you will see:** the same checks and plan, then
`Start this local plan? [y/N]`. Decline, EOF or noninteractive input without
`--yes` makes no changes. Consent is checked again under the shared lock.
Success prints stack progress, app URLs and commands for status, next and TUI.
Nodes/cache can restart after consent even if a later gate or download fails.

Start exits 0 for success or a ready preview, 1 for a blocked/failed run,
and 2 on a non-Linux host or cloud configuration. It has no `--mode` flag.

### Explicit planning and up

Use these lower-level commands when you already know the configured profile.
Unlike guided start, plain up does not select a profile or run doctor first.

**Read-only.**

```sh
lab plan --mode local
```

**What you will see on a fresh default config:**

```text
local mode, minimal + obs profile, Kubernetes 1.36, gitops revision main
  + registry    not deployed
  + cluster     not deployed
  + gitops      not deployed
  + core        not deployed
  + sample-app  not deployed
  + obs         not deployed
6 to apply, 0 to destroy, 0 unchanged
```

`+` applies and `=` keeps. `-` either destroys or skips; a skipped row says
`skip:` in its reason. Colours distinguish them, but read the reason and
summary even without colour. An existing lab has a different plan.
Review any destruction.

> **Changes the lab:** the next command creates rootless containers and a
> Kubernetes cluster, pulls images and installs workloads. It is not a dry run.
> The default local up does not ask for confirmation unless it destroys stacks.

```sh
lab up --mode local
```

**What you will see:** chart prefetch logs, the plan, then progress lines such
as `◐ cluster: applying` and `● cluster: applied in ...`. Each stack's log path
is printed. Once core is available, application URLs are listed.
The first run depends heavily on download speed; do not assume a fixed duration.

If start or up fails, read the failed stack's log. After fixing the problem,
both retry failed stacks and keep applied ones. To abandon the install,
use the cleanup section. Do not delete state to hide a partial deployment.

## First-hour tour

### 1. Check the result

```sh
lab status --mode local
```

**What you will see:** the profile, GitOps revision, stack state, live node/app
health and app URLs. Expect the six selected stacks applied and `main` as the
revision. Other catalog stacks can remain `not deployed`.
`applied` is saved state; the live health section is the current cluster check.

### 2. Look around without changing stacks

```sh
lab
```

**What you will see:** the TUI, initially from cached state. Use `1` Home,
`2` Stacks, `3` Labs, `4` Drills and `5` Ops. `?` opens key help; `q` quits.
View catalogs and recent logs. Do not press start/up/down keys during this tour.
`6` is reserved, not an implemented Cost screen.

The TUI holds the shared mutation lock unless it opens read-only. Quit it
before using a separate terminal for up or down. Status, plan and doctor
remain non-blocking.

### 3. Check the sample application

```sh
export KUBECONFIG="$HOME/.lab/kubeconfig"
```

**What you will see:** no output. kubectl now uses the local lab's dedicated
file, not `~/.kube/config`.

The Gateway uses a self-signed certificate. Export only its public certificate:

```sh
kubectl -n envoy-gateway-system get secret lab-gateway-tls \
  -o jsonpath='{.data.tls\.crt}' | base64 --decode > "$HOME/.lab/gateway.crt"
```

**What you will see:** no output on success; `~/.lab/gateway.crt` contains a
public PEM certificate. Do not extract or share `tls.key`.

```sh
curl --cacert "$HOME/.lab/gateway.crt" https://podinfo.localtest.me:8443
```

**What you will see:** a successful sample-app response, not a TLS error.
Use the URL from status if you changed routing. A browser needs explicit
trust of this certificate; do not disable TLS verification globally.
Argo CD and Grafana also have URLs, but their login credentials are private.

### 4. Choose what to learn next

```sh
lab next
```

**What you will see:** one suggested command with its reason and effect.
Nothing runs or is recorded. It reads fresh doctor results and local state
without taking the lab lock. A healthy lab gets an eligible exercise; problems
get diagnostics, recovery guidance or a confirmed startup suggestion.

```sh
lab next --json
```

**What you will see:** one object with `schemaVersion`, `kind`, `reason`,
`command`, `effect` and `requiresSudo`. Exit 0 means a valid recommendation,
not a healthy lab. Bad input/report data exits 1; unsupported host/cloud exits 2.
Effects are `read-only`, `writes-state`, `changes-lab` or `host-change`.
Review any mutating suggestion before running it.

Completion selection uses durable summaries. Scratch cleanup/reset clears
current work, not historical completion. `lab labs` and `lab drills` are
read-only list aliases; their counts and bests agree with the Home progress line.
See [state migration](../guides/troubleshooting.md#state-and-progress-migration)
for older files and the limits of imported history.

```sh
lab labs list --mode local
```

**What you will see:** six catalog entries with time, level, mode and progress.
Lab 6 is marked unavailable locally. Listing does not start a lab.
Use [the labs guide](../guides/labs.md) before starting one.

```sh
lab drills list
```

**What you will see:** seven drills with limits, alerts and best times.
It reports missing prerequisite stacks; the default does not include
`secrets` or `sample-db`. Listing does not inject a fault.
Read [the drills guide](../guides/drills.md) first.

```sh
lab --help
```

**What you will see:** the CLI command list and flags. Each subcommand's
`--help` explains its arguments without starting that operation.

## Remote access

If the Linux host is elsewhere, run the TUI there, not on the Mac:

```sh
ssh -t lab-host 'cd ~/devops-lab && lab'
```

**What you will see:** the remote TUI. Replace `lab-host` with your SSH alias.

In a separate client terminal:

```sh
ssh -N -L 127.0.0.1:8080:127.0.0.1:8080 \
  -L 127.0.0.1:8443:127.0.0.1:8443 lab-host
```

**What you will see:** normally no output; the SSH process stays open and
forwards only loopback ports. Ctrl-C stops this tunnel, not the lab.
Trust the exported public certificate on the client before browsing.
Do not bind these ports to the LAN.

## Clean up

Quit the TUI and finish or abort any active exercise first.

**Read-only teardown preview.**

```sh
lab plan --mode local --destroy
```

**What you will see:** `-` destruction steps in reverse dependency order.
No resource is deleted by the preview.

> **Destructive:** down removes the lab cluster and deployed stacks, including
> their data. It keeps the registry's image cache volume by default.
> Do not add `--purge` without separate approval to remove that volume.

```sh
lab down --mode local
```

**What you will see:** the destroy plan, `Destroy ... stacks?`, then progress
after you confirm. Stopped main nodes may be started so teardown can reach them.

```sh
lab status --mode local
```

**What you will see:** no deployed stacks after complete cleanup. The image
cache volume remains; that is expected. A failed down needs investigation,
not deletion of its state file.
