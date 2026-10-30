<!-- Exported from the Claude Docs design doc "DevOps Lab Control Center — Plan and Design". The live doc is the source of truth; its two diagrams (architecture, roadmap) appear only there. -->

# DevOps Lab Control Center — Plan and Design

Oct 30, 2026 · @Chandan Kumar

## Summary

Build one lab with one control center: a Go terminal app called `lab` that brings up the same DevOps platform either on the Fedora box (local mode) or on AWS (cloud mode), with the same screens, keys and lab exercises in both.

- **Local mode** runs everything on the ASUS Zephyrus (Fedora 45): kind clusters on rootless Podman, LocalStack for AWS APIs, local registry, full observability. Cost is zero, so it is the default.
- **Cloud mode** runs on real AWS (EKS, ECR, Secrets Manager, Karpenter) in ap-south-1, built with Terraform, with hard cost guardrails: every environment has a TTL and is destroyed automatically.
- **The control center** is a k9s-style TUI built with Go and Bubble Tea. It never blocks on a command, watches the cluster instead of polling it, and opens in well under a second. Every TUI action is also a CLI command, so labs can be scripted and run in CI.

The Mac stays the keyboard and screen. The TUI runs on Fedora and is reached with `ssh -t fedora lab`, which keeps all workloads on Linux as planned. Lab web apps are reached from the Mac browser through an SSH tunnel (`ssh -N fedora-lab`); nothing listens on the LAN.

## Goals, non-goals and principles

The lab exists to practise the Senior/Staff platform stack on real systems, rehearse incidents, and produce public proof of work.

**Goals**

1. One command to bring a full platform up or down: `lab up`, `lab down`, in either mode.
2. Same lab exercises in both modes, so a lab written once runs locally for free and on AWS when it matters.
3. Break/fix drills: inject faults (bad deploy, expired cert, OOM, node loss, DNS failure) and time the recovery, for SRE interview practice.
4. Host the flagship AI On-Call Assistant project and the devops-roadmap labs.
5. A TUI that feels as fast as k9s and needs no manual to use.

**Non-goals**

- Not a production platform or a multi-user portal. One operator, one home lab.
- Not a replacement for k9s. Cluster browsing hands off to k9s on the lab context.
- No Windows support. Linux (Fedora) runs the lab; macOS only runs the client.

**Design principles**

- **Mode is a setting, not a fork.** Modules declare what they need (cluster, registry, secrets store, object store); each mode supplies it. No module has `if local` logic.
- **GitOps after bootstrap.** The tool creates the cluster and installs Argo CD. Everything else is an Argo CD Application from Git, so the lab teaches the real pattern.
- **Never block the screen.** Every action runs in the background and streams output to a pane. Keys stay responsive while Terraform runs.
- **Destroy is cheap and safe.** Any environment can be rebuilt from Git in minutes, so nothing in the lab is precious.
- **Generic and public-ready.** No company names, code or data, so the private repo can be made public for students later.

## Lab modes

Both modes provide the same capabilities; only the provider behind each one changes. LocalStack sits inside local mode, which matches the devops-roadmap decision to offer every cloud lab on LocalStack and on real AWS.

| Capability | Local mode (Fedora) | Cloud mode (AWS, ap-south-1) |
| --- | --- | --- |
| Kubernetes | kind, 1 control plane + 2 workers, on rootless Podman | EKS, managed node group + Karpenter (spot) |
| Infrastructure as code | Terraform against LocalStack | Terraform against AWS, state in S3 with lock |
| Container registry | Zot (light) or Harbor (optional module) | ECR |
| Secrets | External Secrets Operator + LocalStack Secrets Manager | External Secrets Operator + AWS Secrets Manager via EKS Pod Identity |
| Ingress and DNS | Gateway API (Envoy Gateway) on `127.0.0.1:8080/8443` of the Fedora box only; apps are `https://<app>.localtest.me:8443`, reached from the Mac through `ssh -N fedora-lab` | Gateway API + AWS Load Balancer Controller, Route 53 sub-zone (optional) |
| Object storage | LocalStack S3 (or MinIO) | S3 |
| Load and AI | Ollama on the G14 GPU, small models | GPU spot node via Karpenter, only when a lab asks for it |
| Cost | Zero | Pay per hour; TTL and budget enforced (see guardrails) |
| Typical spin-up | About 2 to 4 minutes | About 15 to 20 minutes (EKS control plane) |

Spin-up times are estimates to confirm in phase 1 and phase 3. **When to use which:** local mode for daily practice, module development and break/fix drills; cloud mode for IAM, Karpenter, load balancers, spot behaviour and anything that needs real AWS, usually a few hours at a time before an interview or a demo.

### Memory budget on the Fedora box

The G14 has 22 GiB of RAM and 8 GiB of swap, so the default `standard` profile keeps the lab near 12 GiB and leaves room for the desktop and builds. Figures are estimates to measure in phase 1.

| Profile | Stacks | Estimated RAM |
| --- | --- | --- |
| minimal | registry (Zot), cluster, gitops, core, sample-app | About 4 GiB |
| standard (default) | minimal + secrets (with LocalStack), obs, policy, scaling | About 10 to 12 GiB |
| full | standard + ci, chaos, ai (Ollama with a 3B model on the 4 GB GPU) | About 16 to 19 GiB; close heavy desktop apps |

Harbor adds about 2 GiB, so it runs only in labs that need it. Argo CD and Flux never run together except in the comparison lab.

## Architecture

The TUI is a thin view over one engine; the engine hands work to a local or an AWS provider, and after bootstrap Argo CD pulls every stack from Git, so both clusters end up identical apart from their overlays.

> Diagram: lab architecture · one engine, two providers (see the live design doc)

- **Engine** resolves stack dependencies, runs plan, apply and destroy in order, and writes state to `~/.lab/state.json`. The CLI and the TUI both call it, so they never disagree.
- **Providers** implement one Go interface: `Cluster()`, `Registry()`, `Secrets()`, `ObjectStore()`, `Cost()`, `Destroy()`. Adding a third target later (for example k3s on a spare machine) means one new provider.
- **Remote use:** the binary runs on Fedora; from the Mac, `ssh -t fedora lab` opens it. A later option is serving the TUI over SSH with Charm's Wish, so no shell login is needed.

### Reaching lab apps from the Mac

The Gateway is never exposed on the LAN. The kind control plane publishes Envoy's NodePorts 30080 and 30443 on `127.0.0.1:8080` and `127.0.0.1:8443` of the Fedora box, and the Mac forwards those two ports over SSH.

- **Names:** every app is `https://<app>.localtest.me:8443`. `localtest.me` and all its subdomains resolve to `127.0.0.1` in public DNS, so the same URL works on Fedora and, through the tunnel, on the Mac, with no DNS server or hosts-file edits. If a network's DNS refuses answers that point at `127.0.0.1` (rebinding protection), add the names to the Mac's `/etc/hosts`.
- **TLS:** cert-manager issues a self-signed certificate for `localtest.me` and `*.localtest.me`; the HTTPS listener terminates TLS with it. Browsers warn until the lab CA is trusted on the Mac.
- **Routes:** each app attaches an HTTPRoute to the `lab` Gateway (`envoy-gateway-system`) with its `*.localtest.me` hostname. The first is Argo CD's UI at `https://argocd.localtest.me:8443`. The domain lives only in `gitops/overlays/local`; cloud mode uses its own.
- **`lab up` output:** once the core stack is up, `lab up` lists the URL of every HTTPRoute on the Gateway and prints a one-line reminder to run `ssh -N fedora-lab` on the Mac.
- **Tunnel:** a dedicated entry in the Mac's `~/.ssh/config`, separate from `fedora` so the TUI session never competes for the ports. Run `ssh -N fedora-lab` and leave it open while browsing.

```text
Host fedora-lab
  HostName asuszephyrus          # the Fedora box; same address as the fedora entry
  LocalForward 8080 127.0.0.1:8080
  LocalForward 8443 127.0.0.1:8443
  ExitOnForwardFailure yes       # fail fast if 8080/8443 are taken on the Mac
  ServerAliveInterval 30
```

`LocalForward` listens on the Mac's loopback for both IPv4 and IPv6, so browsers that try `::1` first (localtest.me has both records) still reach the tunnel.

## TUI design

Build the control center in Go with Bubble Tea, Bubbles and Lip Gloss, plus Cobra for the CLI. It gives a single static binary, k9s-class speed, and it is the same Go you need for the Kubernetes operator in the upskilling plan.

| Option | Speed | Fit for this lab | Verdict |
| --- | --- | --- | --- |
| Go + Bubble Tea | Native binary, instant start | client-go, Helm SDK, kind library, terraform-exec, AWS SDK all in Go; builds Go skills | **Chosen** |
| Rust + Ratatui | Fastest rendering | Kubernetes and Helm libraries weaker; Rust is not on the plan | Rejected |
| Python + Textual | Slower start, needs a runtime | Fast to write, but heavier and harder to ship | Rejected |

### Layout

One screen, four fixed zones: header (mode, context, health, cost, TTL), tabs, a list and detail split, and an always-visible operations strip.

```text
 lab  mode: local  ctx: kind-lab  health: ● 14/15  cost: ₹0  ttl: —
 1 Home  2 Stacks  3 Labs  4 Drills  5 Ops log  6 Cost        ? help  : cmd
───────────────────────────────┬─────────────────────────────────────────────
 Stacks                        │ argocd                              Healthy
 ● core     kind cluster       │ ns argocd · chart argo-cd · synced 2m ago
 ● gitops   argocd             │ apps 12 synced · 0 out of sync
 ● obs      prom/grafana/loki  │
 ◐ policy   kyverno  syncing   │ u up  d down  r resync  o open  K k9s
 ○ ai       ollama             │
───────────────────────────────┴─────────────────────────────────────────────
 ops ▸ helm upgrade kyverno · 3/5 resources ready                     00:42
```

### Screens

1. **Home**: mode, cluster health, running stacks, TTL countdown, last five operations. Empty state says which key to press next.
2. **Stacks**: the module catalog with install state, dependencies, endpoints and versions.
3. **Labs**: guided exercises, each with steps and a `verify` check that turns a step green.
4. **Drills**: break/fix scenarios. Start a fault, a timer runs, `verify` stops it and records your time.
5. **Ops log**: every operation with its full streamed output and the equivalent CLI command.
6. **Cost** (cloud mode): running resources, hourly rate, spend this session, budget left, TTL.

### Keys

Keys follow k9s habits so there is nothing new to learn.

| Key | Action |
| --- | --- |
| `1`–`6` | Switch screen |
| `:` | Command palette with fuzzy search (`:up obs`, `:mode cloud`, `:drill cert-expiry`) |
| `/` | Filter the current list |
| `enter` / `esc` | Open detail / go back |
| `u` / `U` | Bring up selected stack / everything |
| `d` | Tear down selected stack (asks to confirm) |
| `r` | Resync or retry |
| `l` | Stream logs for the selected item |
| `o` | Copy the stack's URL to the Mac clipboard (OSC 52 over SSH) |
| `K` | Open k9s on the lab context; quitting k9s returns to `lab` |
| `s` | Shell into a toolbox pod |
| `m` | Switch mode (cloud asks for confirmation and shows cost) |
| `v` | Run verify for the current lab step or drill |
| `?` / `q` | Help / quit |

### Speed rules

- First frame from cached state on disk before any network call. Target: under 100 ms to first frame.
- Every action runs as a background command and streams messages to the UI; the key loop never waits.
- Watch Kubernetes with client-go informers; never poll `kubectl`.
- Call libraries, not CLIs: Helm SDK, kind library, terraform-exec, AWS SDK v2. Shell-outs only where no library exists.
- Log panes use a ring buffer (5,000 lines) and redraw at most 20 times a second, so memory and CPU stay flat.
- Long lists render only the visible rows.

### Usability rules

- Key hints for the current screen are always in the footer.
- State is shown by symbol and colour together (● healthy, ◐ progressing, ○ off, ✕ failed), so it reads on any terminal theme.
- Destroying a cloud environment requires typing its name.
- Every TUI action logs the matching CLI command, so the TUI also teaches the commands.

## Lab catalog

The lab is a set of stacks; each stack is a module that declares its dependencies and runs in both modes. The tool itself only creates registry, cluster and gitops; every later stack is an Argo CD Application. LocalStack is not a stack: the local provider starts it when a stack such as secrets needs its APIs. `lab up` applies the profile; deployed stacks outside it are removed only with `lab up --prune`.

| Stack | Installs | Depends on | Practises |
| --- | --- | --- | --- |
| registry | Zot pull-through cache (Harbor optional) locally, ECR in cloud | — | Image build, scan, retag-to-promote |
| cluster | kind (1 control plane, 2 workers, containerd mirrors to Zot) or EKS; CNI, StorageClass | registry | Cluster bootstrap, CKA topics |
| gitops | Argo CD (app-of-apps) via the Helm SDK; Flux as the second option, chosen per lab | cluster | GitOps, promotion between environments |
| core | metrics-server, Envoy Gateway (Gateway API), cert-manager, as Argo CD apps | gitops | Cluster add-ons, ingress, TLS |
| ci | GitHub Actions self-hosted runners (Actions Runner Controller); Jenkins as an option | gitops, registry, secrets | Pipelines, shared libraries, runners on Kubernetes |
| secrets | External Secrets Operator, LocalStack or AWS Secrets Manager | gitops | Secret sync, rotation, Pod Identity |
| obs | kube-prometheus-stack, Loki, Tempo, OpenTelemetry Collector, Grafana | gitops | Metrics, logs, traces, SLOs with burn-rate alerts (Sloth) |
| policy | Kyverno with baseline policies | gitops | Admission control, policy as code |
| scaling | KEDA locally; Karpenter in cloud | gitops, obs | Autoscaling, spot handling |
| chaos | Chaos Mesh | obs | Fault injection for drills |
| ai | Ollama locally (G14 GPU), small model on a GPU spot node in cloud; Langfuse | obs, secrets | Model serving, LLM observability, the AI On-Call Assistant |
| sample-app | A small multi-service demo app with a database; ServiceMonitor only when obs is present | core, registry | Target for every lab and drill |

### Labs

Labs are Markdown files with steps and a `verify` script per step, so they can also live in the devops-roadmap repo. The first set:

1. Bootstrap a cluster and install Argo CD from scratch.
2. Build, scan and promote an image from dev to prod by retagging.
3. Add an SLO and a burn-rate alert for the sample app.
4. Rotate a database secret without restarting the app.
5. Write a Kyverno policy that blocks images without a digest.
6. Cloud only: Karpenter consolidation and spot interruption.

### Drills

Each drill injects one fault into the sample app, starts a timer and records time to detect and time to recover.

| Drill | Fault injected | What you should find |
| --- | --- | --- |
| bad-deploy | Image tag that crashes on start | CrashLoopBackOff, roll back through Git |
| cert-expiry | Certificate with a short lifetime | cert-manager renewal failing, issuer misconfigured |
| oom | Memory limit set too low | OOMKilled pods, right-size the limit |
| node-loss | A worker node removed | Rescheduling, PodDisruptionBudget behaviour |
| dns | CoreDNS scaled to zero | Service discovery failures across the app |
| slow-db | Network latency on the database | SLO burn alert fires, trace shows the slow span |
| secret-drift | Secret deleted in the store | External Secrets sync error, app fails to start |

## Configuration and repository layout

One file, `lab.yaml`, describes the lab; profiles switch mode and size without editing modules. Secrets and AWS credentials never go in it.

```yaml
lab:
  name: lab
  mode: local              # local | cloud
  profile: standard        # minimal | standard | full
  gitops: argocd           # argocd | flux

profiles:
  local:
    host: asuszephyrus
    runtime: podman        # rootless, always; KIND_EXPERIMENTAL_PROVIDER=podman
    cluster: { provider: kind, workers: 2, k8sVersion: "1.37" }
    awsEndpoint: http://localhost.localstack.cloud:4566
    registry: zot
    memoryLimitGiB: 14     # lab doctor warns above this
  cloud:
    region: ap-south-1
    cluster: { provider: eks, k8sVersion: "1.37", nodes: spot }
    awsProfile: lab        # existing account; IAM user today, MFA role later
    ttl: 4h                # destroy after this, no exceptions
    budgetMonthlyUSD: 30
    owner: chanupen

repo:                      # what Argo CD syncs from (gitops/bootstrap is the app-of-apps)
  url: git@github.com:chanupen/devops-lab.git        # private; SSH for the deploy key
  revision: main
  sshKeyFile: ~/.ssh/devops-lab-deploy   # read-only deploy key, loaded as a Secret at bootstrap; never committed
```

Versions and the budget figure are placeholders; pin them when phase 1 starts.

### Repository layout

```text
devops-lab/
├── cmd/lab/                # main.go, Cobra commands
├── internal/
│   ├── tui/                # Bubble Tea models, one per screen
│   ├── engine/             # plan, apply, destroy, dependency order
│   ├── provider/
│   │   ├── local/          # kind, Podman, LocalStack, Zot
│   │   └── aws/            # Terraform runner, EKS, ECR, cost, TTL
│   ├── kube/               # client-go informers, health
│   ├── state/              # ~/.lab/state.json, ops history
│   └── drills/             # fault injection and timers
├── infra/terraform/
│   ├── local/              # LocalStack resources
│   └── aws/                # VPC, EKS, ECR, IAM, budgets, TTL reaper
├── gitops/
│   ├── bootstrap/          # Argo CD app-of-apps
│   ├── stacks/             # one folder per stack (Helm values, Kustomize)
│   └── overlays/           # local | cloud differences only
├── labs/                   # lab-01-bootstrap.md + verify scripts
├── drills/                 # drill definitions (YAML)
├── lab.yaml
└── Makefile                # build for linux/amd64 and darwin/amd64
```

Mode differences live only in `gitops/overlays/` and `internal/provider/`. If supporting one mode needs edits anywhere else, the design is leaking and should be fixed.

## Cloud mode guardrails

Cloud mode must be impossible to forget about: every environment has an owner, a TTL and a budget, and is destroyed by AWS itself even if the Fedora box is off.

**Cost controls**

- **TTL on everything.** `lab up --mode cloud` writes an `expires-at` tag on every resource. The TUI shows a countdown and warns at 30 and 10 minutes; `lab extend 1h` adds time.
- **Server-side reaper.** A small Lambda on an EventBridge schedule (every 15 minutes) destroys anything past its `expires-at`. It is created once with the account baseline, not with each lab, so it works when the laptop is closed.
- **AWS Budgets.** Monthly budget with alerts at 50%, 80% and 100% to email; at 100% an action blocks new EC2 launches.
- **Cheap by default.** Spot nodes through Karpenter, no NAT gateway (public subnets with tight security groups for lab nodes), one load balancer shared by all routes through Gateway API, short log retention.
- **Live cost meter.** The Cost screen reads Cost Explorer for actual spend and estimates the current hourly rate from running resources.

**Access and safety**

- The existing AWS account. Every lab resource carries a `lab=true` tag and lives in ap-south-1, so the reaper and `lab down` never touch anything else in the account.
- Today the CLI uses the IAM user `devops` with access keys. Until phase 3, enforce MFA on that user and rotate its keys; in phase 3, `lab` assumes a `lab-admin` role with MFA so day-to-day work uses short-lived credentials.
- Pods reach AWS through EKS Pod Identity; no static keys in the cluster.
- Terraform state in S3 with native lock, versioned and encrypted.
- `lab down` destroys in reverse dependency order, then checks for leftovers by tag (load balancers, ENIs, EBS volumes, Elastic IPs) and lists anything still billed.

EKS clusters bill for the control plane by the hour, so a 4-hour session costs a few US dollars; check current rates on the AWS pricing pages before setting the budget.

## Build roadmap

Local mode comes first and must work end to end by week 3; the TUI, cloud mode and labs build on that. The schedule fits the 12-week upskilling plan at 12 to 15 hours a week, and the lab becomes the home of the flagship AI On-Call Assistant rather than a separate project.

> Diagram: build roadmap · 6 phases, 12 weeks (see the live design doc)

Each gate must pass before the next phase starts. If a phase slips, cut TUI screens before labs: the labs and drills are what interviewers will ask about.

## GPU setup for Ollama (Fedora 45, G14)

Use the RPM Fusion driver with Secure Boot left on, then give rootless Podman the GPU through CDI. Ollama runs as a Podman container on the host, outside kind, and the cluster reaches it over the network. Passing a GPU into kind node containers is fragile, and the lab does not need it.

### 1. Check the GPU and Secure Boot

```bash
lspci | grep -iE 'vga|3d'      # expect AMD iGPU + NVIDIA dGPU
mokutil --sb-state             # SecureBoot enabled -> do step 2
```

This G14 has a **GeForce GTX 1650 Mobile / Max-Q (TU117, Turing)** with 4 GB of VRAM. Turing is supported by the current `akmod-nvidia` branch, so no legacy driver is needed. The 4 GB of VRAM is the real limit: 1B to 3B models in 4-bit quantisation run fully on the GPU; 7B to 8B models only partly fit and spill to the CPU, which is much slower. For larger models, use cloud mode.

### 2. Enroll the akmods signing key (Secure Boot only)

Do not turn Secure Boot off; one key enrollment and one extra reboot is enough.

```bash
sudo dnf install akmods mokutil openssl
sudo kmodgenca -a                                   # create the key if none exists
sudo mokutil --import /etc/pki/akmods/certs/public_key.der
sudo systemctl reboot                               # MokManager: Enroll MOK, enter the password
```

### 3. Install the driver from RPM Fusion

```bash
sudo dnf install \
  https://mirrors.rpmfusion.org/free/fedora/rpmfusion-free-release-$(rpm -E %fedora).noarch.rpm \
  https://mirrors.rpmfusion.org/nonfree/fedora/rpmfusion-nonfree-release-$(rpm -E %fedora).noarch.rpm
sudo dnf upgrade --refresh && sudo systemctl reboot   # boot the newest kernel first
sudo dnf install akmod-nvidia xorg-x11-drv-nvidia-cuda
sudo dnf mark user akmod-nvidia                       # keep autoremove away from it
modinfo -F version nvidia                             # wait until this prints a version
sudo systemctl reboot
nvidia-smi                                            # GPU, driver and CUDA version
```

The module builds in the background after `dnf` returns. Rebooting before `modinfo` prints a version is the most common way to end up with no driver.

### 4. Give rootless Podman the GPU (CDI)

```bash
curl -s -L https://nvidia.github.io/libnvidia-container/stable/rpm/nvidia-container-toolkit.repo \
  | sudo tee /etc/yum.repos.d/nvidia-container-toolkit.repo
sudo dnf install nvidia-container-toolkit
sudo systemctl restart nvidia-cdi-refresh.service     # writes /var/run/cdi/nvidia.yaml
nvidia-ctk cdi list                                   # expect nvidia.com/gpu=all
sudo setsebool -P container_use_devices on            # SELinux: allow device access
podman run --rm --device nvidia.com/gpu=all \
  docker.io/nvidia/cuda:12.6.3-base-ubi9 nvidia-smi
```

From toolkit v1.18 the `nvidia-cdi-refresh` service regenerates the CDI spec after driver updates and reboots, so kernel updates need no manual step. Pin the CUDA test image tag to one that exists when you run it.

### 5. Run Ollama under Podman

```bash
podman run -d --name ollama --device nvidia.com/gpu=all \
  -p 11434:11434 -v ollama:/root/.ollama docker.io/ollama/ollama
podman exec -it ollama ollama run llama3.2:3b         # small model first
podman exec ollama ollama ps                          # PROCESSOR column should say GPU
```

`lab` manages this container as a Quadlet unit (systemd user service), so it starts with the user session. Pods in kind reach it through a Service pointing at the host's address on port 11434.

### What `lab doctor` checks

- `mokutil --sb-state` and that the NVIDIA module is loaded and signed.
- `nvidia-smi` succeeds and reports free VRAM; model size is capped to fit it.
- `nvidia-ctk cdi list` shows `nvidia.com/gpu=all`.
- SELinux boolean `container_use_devices` is on.
- The ollama container answers on port 11434.

On the G14's hybrid graphics, keep the default hybrid mode so the NVIDIA GPU powers down when idle; Ollama wakes it on demand.

Sources: [RPM Fusion NVIDIA howto](https://rpmfusion.org/Howto/NVIDIA), [RPM Fusion Secure Boot howto](https://rpmfusion.org/Howto/Secure%20Boot), [Fedora 45 driver walkthrough](https://computingforgeeks.com/install-nvidia-drivers-fedora/), [NVIDIA Container Toolkit CDI support](https://nvidia.github.io/cloud-native-docs/review/pr-467/container-toolkit/latest/cdi-support.html), [Podman GPU containers](https://oneuptime.com/blog/post/2026-03-18-run-nvidia-gpu-containers-podman/view).

## Risks and open questions

The biggest risk is spending the time on TUI polish instead of labs; build the CLI engine first and keep the TUI a thin layer on top of it.

| Risk | Effect | Mitigation |
| --- | --- | --- |
| TUI polish eats the schedule | No labs, no interview proof | Engine and CLI first; TUI screens only after `lab up` works end to end |
| G14 memory runs out with every stack on | Pods evicted, slow cluster | Stack profiles `minimal`, `standard`, `full`; standard fits in about 12 GiB of the 22 GiB; measure in phase 1 |
| kind on rootless Podman (experimental in kind) | Clusters fail to start | Delegate cgroup v2 controllers to the user, raise inotify limits, load the iptables modules, enable the user podman.socket, map ports above 1024; all checked by `lab doctor` |
| NVIDIA driver on Fedora 45 | Ollama falls back to CPU | Keep the ai stack optional; follow the GPU setup section; lab doctor checks the driver and CDI |
| LocalStack free tier gaps or licence changes | Some local AWS labs stop working | Check current LocalStack terms before phase 2; MinIO as the S3 fallback |
| Forgotten cloud environment | Unexpected AWS bill | TTL tags, server-side reaper, budget action |

**Decisions taken (30 Oct 2026)**

| Decision | Choice |
| --- | --- |
| Repo | Private repo `github.com/chanupen/devops-lab`; kept generic so it can go public later |
| Container runtime | Rootless Podman on Fedora, always; no Docker |
| GitOps | Argo CD by default; Flux as the second option |
| Fedora RAM | 22 GiB + 8 GiB swap; default profile `standard` |
| AWS | Existing account through IAM user `devops`; move to an MFA-assumed role in phase 3 |
| Tool name | `lab` |

**Confirmed later on 30 Oct 2026**

- Cloud region: ap-south-1.
- GPU route: RPM Fusion driver with Secure Boot, rootless Podman through CDI, Ollama as a host container (see GPU setup).
