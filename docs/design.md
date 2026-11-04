<!-- Exported from the Claude Docs design doc "DevOps Lab Control Center — Plan and Design". The live doc is the source of truth; its two diagrams (architecture, roadmap) appear only there. -->

# DevOps Lab Control Center — Plan and Design

Oct 30, 2026 · @Chandan Kumar

## Summary

Build one lab with one control center: a Go terminal app called `lab` that brings up the same DevOps platform either on the Fedora box (local mode) or on AWS (cloud mode), with the same screens, keys and lab exercises in both.

- **Local mode** runs everything on the ASUS Zephyrus (Fedora 45): kind clusters on rootless Podman, Moto for AWS APIs (Secrets Manager), local registry, full observability. Cost is zero, so it is the default.
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

Both modes provide the same capabilities; only the provider behind each one changes. Moto, an AWS API emulator, sits inside local mode, so a lab that uses an AWS API runs the same commands locally and on real AWS; only the endpoint differs.

| Capability | Local mode (Fedora) | Cloud mode (AWS, ap-south-1) |
| --- | --- | --- |
| Kubernetes | kind, 1 control plane + 2 workers, on rootless Podman | EKS, managed node group + Karpenter (spot) |
| Infrastructure as code | Terraform against Moto (where it emulates the APIs) | Terraform against AWS, state in S3 with lock |
| Container registry | Zot (light) or Harbor (optional module) | ECR |
| Secrets | External Secrets Operator + Secrets Manager emulated by Moto | External Secrets Operator + AWS Secrets Manager via EKS Pod Identity |
| Ingress and DNS | Gateway API (Envoy Gateway) on `127.0.0.1:8080/8443` of the Fedora box only; apps are `https://<app>.localtest.me:8443`, reached from the Mac through `ssh -N fedora-lab` | Gateway API + AWS Load Balancer Controller, Route 53 sub-zone (optional) |
| Object storage | Moto S3 (or MinIO) | S3 |
| Load and AI | Ollama on the G14 GPU, small models | GPU spot node via Karpenter, only when a lab asks for it |
| Cost | Zero | Pay per hour; TTL and budget enforced (see guardrails) |
| Typical spin-up | About 2 to 4 minutes | About 15 to 20 minutes (EKS control plane) |

Spin-up times are estimates to confirm in phase 1 and phase 3. **When to use which:** local mode for daily practice, module development and break/fix drills; cloud mode for IAM, Karpenter, load balancers, spot behaviour and anything that needs real AWS, usually a few hours at a time before an interview or a demo.

### Decision: Moto instead of LocalStack (2026-11-03)

Local mode emulates AWS APIs with Moto (`motoserver/moto` 5.2.5, Apache-2.0), pinned by digest, not LocalStack:

- Since 2026-03-23 the `localstack/localstack` image needs an auth token to start (the 2026.x releases).
- The free Hobby plan needs a LocalStack account per user and is for non-commercial use only, which does not fit a repo that may become public and be used by others.
- The last image that runs without a token, 4.14, gets no fixes or security patches.

Moto speaks the Secrets Manager API that ESO's AWS provider uses, so the provider and the lab commands are the same as on AWS; only the endpoint differs. It keeps no state across restarts, so `lab up` re-seeds it from the Kubernetes Secrets ESO keeps. While the secrets stack is deployed, `lab doctor` checks that `lab-moto` runs, answers on 127.0.0.1:4566 and holds secrets; an empty Moto (restarted since the last `lab up`) is a warning, fixed by `lab up`.

### Memory budget on the Fedora box

The G14 has 22 GiB of RAM and 8 GiB of swap. The default is `minimal` plus obs (about 4.6 GiB measured), because those are the stacks the local provider runs today; `standard` keeps the lab near 12 GiB and becomes the default once its stacks exist. Profile figures are estimates; the measured row comes from the phase 1 gate.

| Profile | Stacks | Estimated RAM |
| --- | --- | --- |
| minimal | registry (Zot), cluster, gitops, core, sample-app | About 4 GiB |
| minimal + obs (default) | minimal + obs, through `extraStacks: [obs]` in `lab.yaml` | 4.6 GiB median over three runs (4.3 to 4.8 GiB), `podman stats` on the `lab-*` containers right after every app was Synced and Healthy |
| minimal + obs + policy | the default plus `policy` (Kyverno and the baseline Pod Security policies) | 0.8 GiB more than the default (5.0 to 5.9 GiB, `podman stats` on the `lab-*` containers, median of three); Kyverno's own four pods use about 210 MiB, the rest is the API server and etcd carrying its CRDs, webhooks and reports |
| standard | minimal + secrets (with Moto), obs, policy, scaling | About 10 to 12 GiB |
| full | standard + ci, chaos, ai (Ollama with a 3B model on the 4 GB GPU) | About 16 to 19 GiB; close heavy desktop apps |

Harbor adds about 2 GiB, so it runs only in labs that need it. Argo CD and Flux never run together except in the comparison lab.

### After a reboot

A reboot leaves the lab's containers (`lab-*`: the kind nodes and `lab-zot`) in place but stopped. Every lab container's name starts with `lab-`, so lab finds them through the Podman API:

- `lab up` checks the Kubernetes pin first (see Kubernetes version), then starts the main cluster's nodes and `lab-zot` before anything else, then waits for the nodes if it started them. A stopped scratch cluster is left to `lab labs start`, and any other stopped `lab-*` container is reported with its `podman start` command, not started.
- `lab down` starts the main cluster's nodes and `lab-zot` first too, so destroying the in-cluster stacks can reach the API server.
- `lab labs start` starts `lab-zot` and the scratch cluster's node when they are there but stopped. Without Zot it says images come straight from the registries.
- `lab doctor` checks the main cluster, the scratch clusters and `lab-zot`. A stopped one shows as `○ stopped` with its `podman start` command. Stopped is not a failure.
- The TUI's Home screen shows `○ stopped` and the command for a stopped cluster or `lab-zot`, in the header too. It lists the containers in the background: at start, every five file checks, and when the cluster goes down or comes back.

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

1. **Home**: mode, cluster health (`○ stopped` with the `podman start` command when a reboot stopped it), running stacks, the image cache, TTL countdown, last five operations. Empty state says which key to press next.
2. **Stacks**: the module catalog with install state, dependencies, endpoints and versions.
3. **Labs**: the labs with progress (● done, ◐ current, ○ not started). `enter` opens the current step's text, `v` verifies it, `h` shows its next hint, `n`/`p` move between steps.
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
- `lab up` or `lab down` in another shell shows up within 2 seconds: the TUI checks the modification times of `state.json` and the kubeconfig (a file check, not a cluster call), re-reads the state, and restarts its informers when the cluster changes.
- Call libraries, not CLIs: Helm SDK, kind library, terraform-exec, AWS SDK v2. Shell-outs only where no library exists.
- Log panes use a ring buffer (5,000 lines) and redraw at most 20 times a second, so memory and CPU stay flat.
- Long lists render only the visible rows.

### Usability rules

- Key hints for the current screen are always in the footer.
- State is shown by symbol and colour together (● healthy, ◐ progressing, ○ off, ✕ failed), so it reads on any terminal theme.
- Destroying a cloud environment requires typing its name.
- Every TUI action logs the matching CLI command, so the TUI also teaches the commands.

## Lab catalog

The lab is a set of stacks; each stack is a module that declares its dependencies and runs in both modes. The tool itself only creates registry, cluster and gitops; every later stack is an Argo CD Application. Moto is not a stack: the local provider starts it when a stack such as secrets needs an AWS API. `lab up` applies the profile plus `lab.yaml`'s `extraStacks`; deployed stacks outside it are removed only with `lab up --prune`. A stack the mode's provider cannot run yet, and any stack that needs one, is shown in the plan as `- <stack>  skip: not supported by the local provider yet` with one warning, and the rest is applied; `lab up` never reports a stack as "to apply" and then fails on it.

Chart values live in `gitops/stacks/<stack>/` and local-only values in `gitops/overlays/local/<stack>/`; multi-source Argo CD apps take the chart from its Helm repo and both value files from this repo. Grafana's admin login is a Secret the obs stack generates in the cluster (`monitoring/grafana-admin`, never in Git); read it with `kubectl -n monitoring get secret grafana-admin -o jsonpath='{.data.admin-password}' | base64 -d`.

| Stack | Installs | Depends on | Practises |
| --- | --- | --- | --- |
| registry | Zot pull-through cache (Harbor optional) locally, ECR in cloud | — | Image build, scan, retag-to-promote |
| cluster | kind (1 control plane, 2 workers, containerd mirrors to Zot) or EKS; CNI, StorageClass | registry | Cluster bootstrap, CKA topics |
| gitops | Argo CD (app-of-apps) via the Helm SDK; Flux as the second option, chosen per lab | cluster | GitOps, promotion between environments |
| core | metrics-server, Envoy Gateway (Gateway API), cert-manager, as Argo CD apps | gitops | Cluster add-ons, ingress, TLS |
| ci | GitHub Actions self-hosted runners (Actions Runner Controller); Jenkins as an option | gitops, registry, secrets | Pipelines, shared libraries, runners on Kubernetes |
| secrets | External Secrets Operator; Secrets Manager from Moto locally, AWS in cloud | gitops | Secret sync, rotation, Pod Identity |
| obs | kube-prometheus-stack (Prometheus with 24 h retention, Alertmanager, node-exporter, kube-state-metrics, Grafana at `grafana.localtest.me`), Loki single binary on filesystem storage, the OpenTelemetry Collector shipping pod logs to Loki; Tempo, traces and Sloth come later | gitops, core | Metrics, logs, dashboards and alerts; traces and SLOs with burn-rate alerts later |
| policy | Kyverno 1.19 (chart 3.9.2, one replica per controller, policy exceptions on) and the Pod Security Standards "baseline" policies in Audit mode: they report, never block. Labs add Enforce policies scoped to their own namespaces. Not in the minimal profile: `extraStacks: [obs, policy]` or `lab up --stacks policy` | gitops | Admission control, policy as code |
| scaling | KEDA locally; Karpenter in cloud | gitops, obs | Autoscaling, spot handling |
| chaos | Chaos Mesh | obs | Fault injection for drills |
| ai | Ollama locally (G14 GPU), small model on a GPU spot node in cloud; Langfuse | obs, secrets | Model serving, LLM observability, the AI On-Call Assistant |
| sample-app | podinfo as a frontend calling a backend, pulled through the Zot cache, at `podinfo.localtest.me`; ServiceMonitors only when obs is present. The DB-backed app for the drills is the sample-db stack | core, registry | Target for every lab and drill |
| sample-db | Postgres 18 and a small Go app (`apps/sample-db`) that reads its database credentials from a mounted Secret file (no env vars, no subPath), reloads them when the file changes and reconnects; `/healthz`, `/readyz`, Prometheus metrics (DB errors, reconnects, credential reloads). The credentials come from Secrets Manager through ESO: a superuser (`lab/sample-db/admin`, never rotated) and the app's login (`lab/sample-db/app`, `app_v1`, a member of the NOLOGIN group role `app`). lab builds the image from the repo through Podman's API, pushes it to the lab registry and sets it by digest (`lab-zot:5000/lab/sample-db@sha256:...`), so the cluster needs no registry credentials | secrets, registry | Secret rotation without restarts, the app the drills break |

### Labs

Each lab is a directory, `labs/NN-name/`, in a generic format that can be copied into the devops-roadmap repo as is (`labs/README.md` describes it):

- `lab.yaml`: id, title, time, level, modes, `needs` and the tools the steps use.
- `README.md`: an intro, then `## Step N: title` sections, each with the task, `### Hints` and `### Expected result`.
- `steps/NN-verify.sh`: one per step. Read-only and idempotent; exit 0 means done, otherwise the last line says why not.
- `reset.sh`: returns the cluster to the lab's start.
- `solution/NN-solve.sh`: a reference answer per step, plus the files it applies. Shown only on request.

A lab says where it runs. `needs: scratch` gives it its own single-node kind cluster, `lab-scratch-<id>`, on rootless Podman. It sits on the kind network and pulls through the Zot cache when that runs, but has no host ports and its own kubeconfig, so it never touches the main lab or `~/.kube/config`. `lab labs stop` deletes it. `needs: lab` runs on the main lab's cluster and lists the stacks it needs.

A lab on the main cluster works only in its own namespaces, `labNN-*`, and labels any cluster-scoped object it creates `lab.devops/lab=<id>`; its `reset.sh` removes exactly those. Images a lab builds go to the lab registry: a writable namespace in the main lab's Zot, `lab/`, apart from the pull-through cache (whose repositories are all under an upstream's name). The host pushes to `127.0.0.1:5000/lab` (Zot's port is published on 127.0.0.1 only, never the LAN) and the cluster pulls `lab-zot:5000/lab` over plain HTTP, through a `hosts.toml` beside the mirror files that `lab labs start` (and `lab up`) writes; the nodes read it on every pull, so it applies to a running cluster. `lab doctor` checks that a push is accepted and the cluster can pull. The scripts read the addresses from `LAB_REGISTRY_PUSH` and `LAB_REGISTRY_PULL`, so cloud mode needs only an overlay (ECR). Tools a lab needs beyond kubectl, jq and curl (Trivy, Skopeo, Sloth, the kyverno CLI) run as images pinned by digest through rootless Podman, so nothing is installed on the host.

`lab labs list | start <id> | status | verify [step] | hint [step] | solution <step> | reset | stop`. `start`, `stop` and `reset` take the lab lock; `solution` and `reset` ask first. Verify scripts run with `KUBECONFIG` set to the lab's cluster and a 60-second timeout. Progress lives in `~/.lab/state.json`: the step reached, attempts and hints per step, solutions shown and time spent. Only one lab is started at a time.

- **Time spent** counts only while the lab is started and its cluster's node runs, from the node's start and stop times in Podman. A node that a reboot stopped adds nothing after it stopped. `start` adds the session so far to the total and opens a new one. `stop` prints how far the lab got ("stopped at step 4/6 after 32m"). For a scratch lab it deletes the cluster and clears the lab's progress, steps and time, because the work went with the cluster; the next start begins at step 1. A lab on the main cluster keeps its steps and starts its time from zero.
- **`list`** shows the current step: `step 1/6, 0m, started` right after a start, `done, 6/6 steps, 42m` at the end.
- **`start` output** is a few progress lines (image cache, creating the cluster, kubeconfig, nodes ready) and then the step. kind's own output and node diagnostics go to the op log only; `-v`/`--verbose` on `start` and `stop` streams them too.
- **client-go messages** (a watch that fails while a new cluster's RBAC comes up) go to the op log, never the terminal: every wait gives its informer a logger that writes to the step's log (for `lab labs start`, the op log's detail). The informer retries on its own.

`go test -tags e2e ./internal/labs/` runs the labs for real: each step's verify fails before its solution and passes after it (twice), then reset. Lab 1 runs on its own scratch cluster. The labs on the main cluster run only with `LAB_E2E_MAIN_KUBECONFIG` set to a running lab's kubeconfig, and only touch their own namespaces and labelled objects.

The first set:

1. Bootstrap Argo CD from scratch (scratch cluster, about 45 minutes): install with Helm, log in through a port-forward, deploy podinfo from Git, turn on auto-sync, watch self-heal undo drift, then an app of apps.
2. Build, scan and promote an image by retagging (main cluster, about 40 minutes): build with rootless Podman on an alpine base with known CVEs, push to the lab registry as dev, scan with Trivy and move to a patched base, deploy to `lab02-dev` by digest, promote to `lab02-prod` by copying the digest with Skopeo (no rebuild), and record the scanned digest both environments run.
3. SLO and burn-rate alerts for the sample app (main cluster, about 45 minutes): pick podinfo-frontend's 5xx share as the SLI, record it, set a 99.5%/30d SLO, write the SRE workbook's three multiwindow alerts as a PrometheusRule (14.4x over 1h/5m and 6x over 6h/30m page, 1x over 3d/6h is a ticket), inject errors with a Job, watch the page fire in Alertmanager and silence it, then compare with Sloth's rules. The lab windows are shorter (10m/2m, 1h/10m and 12h/1h), so the alerts fire in minutes; the README says how that differs from production, and why the ticket fires so soon here (a quiet service).
4. Rotate a database password without restarting the app (main cluster, needs `secrets` and `sample-db`, about 40 minutes): deploy a copy of the sample-db app in `lab04-app` with its credentials in Secrets Manager (Moto locally), start a traffic Job that counts failed requests, add a second login role (`app_v2`, a member of the group role `app`), update the secret with `put-secret-value`, force an ESO sync and watch the kubelet update the mounted file and the app switch in the same pod (same UID, restartCount 0), retire `app_v1` with `NOLOGIN` and prove its old password (`AWSPREVIOUS`) is rejected, with zero failed requests. The README explains why environment variables and subPath mounts cannot carry a rotation.
5. A Kyverno policy that blocks images without a digest (main cluster, needs `policy`, about 35 minutes): a ValidatingPolicy (CEL) that denies tag-only images in `lab05-*`, a tag-only Pod denied in a server-side dry run, a digest Pod allowed, a PolicyException for one named Deployment and the Pods it owns, the baseline policies' Audit results in PolicyReports, and `kyverno test` offline. The README covers ClusterPolicy, the older form still common in clusters, in one paragraph.
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

One file, `lab.yaml`, describes the lab; profiles switch mode and size without editing modules. Secrets and AWS credentials never go in it. Without `--config`, `lab` reads `./lab.yaml`, then `~/.lab/lab.yaml`; a symlink there to the repo's file lets `ssh -t fedora lab`, which starts in the home directory, use the same config. The TUI falls back to the defaults (minimal + obs, no repo) and says so on Home.

```yaml
lab:
  name: lab
  mode: local              # local | cloud
  profile: minimal         # minimal | standard | full
  extraStacks: [obs]       # applied on top of the profile
  gitops: argocd           # argocd | flux

profiles:
  local:
    host: asuszephyrus
    runtime: podman        # rootless, always; KIND_EXPERIMENTAL_PROVIDER=podman
    cluster: { provider: kind, workers: 2, k8sVersion: "1.36" }
    awsEndpoint: http://127.0.0.1:4566   # Moto
    registry: zot
    memoryLimitGiB: 14     # lab doctor warns above this
  cloud:
    region: ap-south-1
    cluster: { provider: eks, k8sVersion: "1.36", nodes: spot }
    awsProfile: lab        # existing account; IAM user today, MFA role later
    ttl: 4h                # destroy after this, no exceptions
    budgetMonthlyUSD: 30
    owner: chanupen

repo:                      # what Argo CD syncs from (gitops/bootstrap is the app-of-apps)
  url: git@github.com:chanupen/devops-lab.git        # private; SSH for the deploy key; lab-bootstrap tracks main
  sshKeyFile: ~/.ssh/devops-lab-deploy   # read-only deploy key, loaded as a Secret at bootstrap; never committed
```

Versions and the budget figure are placeholders; pin them when phase 1 starts.

### Kubernetes version

Both modes check their Kubernetes version against one compatibility table, `internal/compat`: for each pinned component, the range its upstream states for the release lab installs, and the modes that install it. Argo CD 3.5 (tested on 1.33 to 1.36) and Kyverno 1.19 are in both modes, Karpenter and the Load Balancer Controller in cloud mode, and the kind v0.33.0 node images (1.34 to 1.37) in local mode. External Secrets Operator 2.13 (secrets stack, both modes) states one version, 1.36; ESO supports only its newest minor. Moto runs beside the cluster, so its row carries no range (`OutsideCluster`). Today that holds both labs at 1.36. Kyverno's releases page lists 1.33 to 1.35 as supported, but its release-1.19 conformance tests run 1.33 to 1.36; the row uses the tested range, cites both, and marks 1.36 as tested, not stated (`StatedMaxK8s`). Each row names the stack that installs it, and a row limits the version only while its stack is active: in the profile or `extraStacks`, asked for with `lab up --stacks`, or deployed, with what they depend on. That applies to the local `k8sVersion` check and to the EKS version choice. `lab doctor` warns (never fails) when a deployed stack's component supports the pinned version only by upstream's tests: today, the policy stack on 1.36. No newer Kyverno states 1.36 yet (v1.20.0 is a release candidate).

- **Local:** `lab.yaml` `k8sVersion` (default 1.36) picks the kind node image, by digest from kind's release notes, for the main cluster and the scratch clusters. `lab up` refuses a pin outside the table and says which component does not support it and which versions work. It also refuses when the main cluster runs another version than the pin: a kind cluster cannot change version in place, so `lab down`, then `lab up` makes it again (the image cache volume is kept).
- **The running version comes from the node:** `/kind/version` in the control-plane container, read through the Podman exec API. The node's image name cannot be used: kind's Podman provider pulls by digest, so the name has no tag. A stopped node cannot be exec'd, so `lab up` checks the pin against the table first, then starts stopped containers, then reads the version. A version it cannot read refuses, like a mismatch; it never passes. `lab labs start` does the same for an existing scratch cluster (fix: `lab labs stop`, then `lab labs start`).
- **Shown live:** `lab status` (a `version` line), the TUI's Home screen (a `version` row) and `lab doctor` (the `lab cluster` check) show the node's version: ● on the pin, ✕ with `lab down, then lab up` when it differs. An unreadable version is ✕ in status and Home and a warning in doctor.
- **Cloud:** lab picks the newest EKS version in standard support that the table's cloud rows all support, or `cloud.k8sVersion`; a running cluster keeps its version.

### GitOps revision

The revision is deliberately not in `lab.yaml`: `lab-bootstrap` and the in-repo apps it renders track `main`, so work reaches the lab by merging.

- **Testing a branch:** `lab up --revision <branch>` syncs the gitops stack from that branch, so GitOps changes can be tried before they merge. The plan warns that gitops will not track `main`.
- **State:** `~/.lab/state.json` records the revision the gitops stack applied. A plain `lab up` (no `--revision`) re-applies gitops whenever the recorded revision is not `main`, which moves the root app back.
- **`lab status`:** lists every stack with its state and the revision (highlighted when it is not `main`). The header's Kubernetes version is the `lab.yaml` pin. Once the cluster stack is applied it adds live health from one look at the cluster (5-second timeout): nodes Ready, in local mode the node's Kubernetes version (✕ with the fix when it is not the pin), Argo CD apps Synced and Healthy, and each app that is not, with its sync and health status. A cluster that does not answer shows as off with the reason, or as stopped with the command that starts it. Once core is up it lists the app URLs. It changes nothing.

### Repository layout

```text
devops-lab/
├── cmd/lab/                # main.go, Cobra commands
├── internal/
│   ├── tui/                # Bubble Tea models, one per screen
│   ├── engine/             # plan, apply, destroy, dependency order
│   ├── provider/
│   │   ├── local/          # kind, Podman, Moto, Zot
│   │   └── aws/            # Terraform runner, EKS, ECR, cost, TTL
│   ├── kube/               # client-go informers, health
│   ├── state/              # ~/.lab/state.json, ops history
│   └── drills/             # fault injection and timers
├── infra/terraform/
│   ├── local/              # Moto resources
│   └── aws/                # VPC, EKS, ECR, IAM, budgets, TTL reaper
├── gitops/
│   ├── bootstrap/          # Argo CD app-of-apps
│   ├── stacks/             # one folder per stack (Helm values, Kustomize)
│   └── overlays/           # local | cloud differences only
├── labs/                   # NN-name/: lab.yaml, README.md, steps/, reset.sh, solution/
├── drills/                 # drill definitions (YAML)
├── lab.yaml
└── Makefile                # make install; build-all for linux/amd64 and darwin/amd64
```

**Build and install:** `make install` builds `bin/lab` and installs it as `$(BINDIR)/lab`, with `BINDIR` defaulting to `~/.local/bin` (`make install BINDIR=/some/dir` for another place; no sudo). It prints the installed path and `lab version`. It warns, without failing, when `BINDIR` is not on `PATH`, or when `command -v lab` finds another copy that runs instead, and prints that copy's path. `make uninstall` removes `$(BINDIR)/lab` only. `make help` lists the targets. `make build` alone leaves the binary in `bin/lab`, so an older `lab` on `PATH` keeps running.

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

- The existing AWS account. Every lab resource carries a `lab=true` tag and lives in ap-south-1, so the reaper and `lab down` never touch anything else in the account. The one exception is the lab VPC: once it has expired, the reaper also deletes untagged load balancers, target groups, network interfaces and security groups inside it, because anything there belongs to the lab.
- The `devops` IAM user keeps its access keys only as the source profile; day-to-day work uses the `lab` profile, which assumes `lab-admin` with MFA for short-lived credentials (see Account baseline).
- Pods reach AWS through EKS Pod Identity; no static keys in the cluster.
- Terraform state in S3 with native lock, versioned and encrypted.
- `lab down` destroys in reverse dependency order, then checks for leftovers by tag (load balancers, ENIs, EBS volumes, Elastic IPs) and lists anything still billed. Each tagged resource is looked up directly, because the tagging API lists deleted resources for up to an hour; terminated or missing ones are listed apart as stale index entries and ignored. The teardown build's final email does the same.

EKS clusters bill for the control plane by the hour, so a 4-hour session costs a few US dollars; check current rates on the AWS pricing pages before setting the budget.

### Account baseline

`infra/terraform/aws/baseline`, applied with OpenTofu by `lab cloud baseline plan|apply` (terraform-exec), holds what lives in the account for good:

- **State bucket** `lab-tofu-state-<account>`: versioned, AES-256 encrypted, public access blocked, TLS-only policy, old versions kept 90 days. Locking is native S3 (`use_lockfile`, OpenTofu ≥ 1.10), so there is no DynamoDB table. The first apply runs with local state in `~/.lab/tofu/baseline`; once the bucket exists, lab writes the S3 backend and `tofu init -force-copy` moves the state into it. lab picks the backend by whether the bucket exists, so a new machine uses the bucket's state, never a fresh local one.
- **`lab-admin` role**: assumable only by the `devops` user and only with MFA (`aws:MultiFactorAuthPresent`), at most 4 hours. The `lab` CLI profile assumes it (`role_arn`, `mfa_serial`, `source_profile = devops`); `lab cloud login` asks for the code once and caches the session under `~/.lab/aws` until shortly before it expires, and lab passes those temporary keys to tofu, which cannot prompt. The very first apply runs with `--profile devops`, before the role exists.
- **Budget `lab-monthly`**: $30 a month, email alerts at 50, 80 and 100% of actual spend. At 100% a budget action attaches `lab-budget-deny-run-instances` (deny `ec2:RunInstances`) to `lab-admin`, through a role that may attach and detach only that policy on that role. Detach it in the console or with `aws iam detach-role-policy` after raising the budget.
- **TTL reaper** `lab-ttl-reaper`: a Go Lambda (arm64, `make reaper`) on a 15-minute EventBridge schedule. It lists `lab=true` resources with the tagging API and deletes those whose `expires-at` has passed, node groups and instances first, then load balancers, EBS volumes and Elastic IPs. Inside an expired lab VPC it also deletes every load balancer (Classic too), target group, unattached network interface and non-default security group, tagged or not. For tofu-managed resources it starts the CodeBuild teardown, again while they remain, at most 6 times, then emails "manual action needed". It confirms before acting, because the tagging API lists deleted resources for up to an hour: each expired resource is looked up directly, and it starts a build only for a stack whose OpenTofu state holds resources and whose teardown bundle's `expires_at` has passed. The build checks the bundle again, and the state lock, before it sweeps or destroys, and `lab up --mode cloud` refuses while a build runs, so a teardown never races a new lab. It fails toward teardown: when it cannot read its attempt count (`teardown/state.json`) it starts the build anyway and reports the error. Each scheduled run also tests itself (it can read `teardown/` and see the `lab-teardown` project) and writes a heartbeat, clean or not; `lab up --mode cloud` and `lab doctor --mode cloud` accept only a recent clean one. A run that returns an error emails the owner through a Lambda on-failure destination (SNS `lab-teardown`). It starts in dry-run mode: it only logs what it would delete, and its role has no delete permissions at all until `reaper_dry_run = false`.

Baseline resources carry `lab=true`, `owner` and `component=baseline` but **no `expires-at`**: they are permanent, and the reaper keeps anything without one. Every lab environment resource still gets `expires-at`. The alert email lives in `~/.lab/aws/baseline.tfvars`, outside the repo; `lab doctor --mode cloud` checks OpenTofu, that the `lab` profile assumes `lab-admin`, and that the bucket and budget exist.

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
| G14 memory runs out with every stack on | Pods evicted, slow cluster | Stack profiles `minimal`, `standard`, `full`; the default minimal + obs measured 4.6 GiB; standard fits in about 12 GiB of the 22 GiB |
| kind on rootless Podman (experimental in kind) | Clusters fail to start | Delegate cgroup v2 controllers to the user, raise inotify limits, load the iptables modules, enable the user podman.socket, map ports above 1024; all checked by `lab doctor` |
| NVIDIA driver on Fedora 45 | Ollama falls back to CPU | Keep the ai stack optional; follow the GPU setup section; lab doctor checks the driver and CDI |
| Moto is a test emulator, not a service | Some AWS behaviour is missing (no IAM enforcement, no persistence) | Labs use only the APIs Moto covers; `lab up` re-seeds it after a restart; the real behaviour is checked in cloud mode |
| Forgotten cloud environment | Unexpected AWS bill | TTL tags, server-side reaper, budget action |

**Decisions taken (30 Oct 2026)**

| Decision | Choice |
| --- | --- |
| Repo | Private repo `github.com/chanupen/devops-lab`; kept generic so it can go public later |
| Container runtime | Rootless Podman on Fedora, always; no Docker |
| GitOps | Argo CD by default; Flux as the second option |
| Fedora RAM | 22 GiB + 8 GiB swap; default profile `minimal` + obs |
| AWS | Existing account through IAM user `devops`; move to an MFA-assumed role in phase 3 |
| Tool name | `lab` |

**Confirmed later on 30 Oct 2026**

- Cloud region: ap-south-1.
- GPU route: RPM Fusion driver with Secure Boot, rootless Podman through CDI, Ollama as a host container (see GPU setup).
