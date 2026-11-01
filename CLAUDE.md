# devops-lab

A DevOps/SRE home lab with two modes (local and cloud), driven by one Go binary called `lab`: a fast, k9s-style TUI plus a CLI. Every TUI action is also a CLI command.

Design doc: "DevOps Lab Control Center — Plan and Design" (Claude Docs). Export it to `docs/design.md` and keep it in sync.

## Hard rules

- **Container runtime is rootless Podman, always.** Never install or call Docker. kind runs with `KIND_EXPERIMENTAL_PROVIDER=podman`.
- Keep everything generic: no company names, code or data. The repo is private today but must be publishable later.
- Never commit secrets, AWS keys, kubeconfigs or Terraform state.
- Ask before anything destructive on the host (deleting files outside the repo, `sudo` changes, removing Podman volumes) and before any `terraform apply`/`destroy` against AWS.
- Cloud work stays in **ap-south-1** and every AWS resource gets the tags `lab=true`, `owner=chanupen`, `expires-at=<RFC3339>`.

## Hosts

- Fedora 45 on ASUS Zephyrus G14 (`asuszephyrus`): runs all workloads. 22 GiB RAM, 8 GiB swap.
  - GPU: GeForce GTX 1650 Mobile (TU117, Turing), 4 GB VRAM, next to an AMD iGPU. Driver `akmod-nvidia` (RPM Fusion), working.
  - Rootless Podman gets the GPU through CDI: `--device nvidia.com/gpu=all`. SELinux boolean `container_use_devices` is on.
  - Ollama runs as a Quadlet user service: `~/.config/containers/systemd/ollama.container`, port 11434 (localhost only, no auth). Models must fit about 3.6 GiB VRAM (1B–3B, 4-bit).
- MacBook Pro (Intel, `macbookprosys`): client only. Reaches the TUI with `ssh -t fedora lab`, and lab apps (`https://<app>.localtest.me:8443`) through `ssh -N fedora-lab`, which forwards 8080/8443 to Fedora's 127.0.0.1. The Gateway never listens on the LAN.

## Architecture

- `lab` TUI (Bubble Tea + Bubbles + Lip Gloss) → engine (plan, apply, destroy, dependency order, state in `~/.lab/state.json`) → providers.
  - Only one lab changes things at a time: `up`, `down` and a TUI that can act take a non-blocking flock on `~/.lab/state.json.lab.lock` (a TUI for as long as it is open). A second `up`/`down` exits at once with "lab and its components are already running: <holder> (pid, terminal, since)". Read-only commands (`status`, `plan`, `doctor`) and the TUI's views and reports never take it. A TUI that cannot get the lock opens read-only ("read-only: another lab is running (pid …)" in the header, actions refused) and takes over once the holder is done. Inside a TUI, actions run one at a time.
  - Stacks a provider cannot run yet are skipped at plan time (`- <stack>  skip: …`, one warning), never failed at run time.
  - Without `--config`, `lab` reads `./lab.yaml`, then `~/.lab/lab.yaml` (on Fedora a symlink to the repo's file, so `ssh -t fedora lab` gets the real config).
- Providers implement one Go interface: `Cluster()`, `Registry()`, `Secrets()`, `ObjectStore()`, `Cost()`, `Destroy()`.
  - `local`: kind on rootless Podman, LocalStack, Zot registry, Ollama on the host.
  - `aws`: Terraform via terraform-exec, AWS SDK v2, EKS + Karpenter (spot), ECR, Secrets Manager, EKS Pod Identity.
- After bootstrap, GitOps owns everything: **Argo CD by default, Flux as the second option** (`gitops: argocd | flux`). Never run both except in the comparison lab.
- Mode differences live only in `gitops/overlays/` and `internal/provider/`. If supporting one mode needs changes anywhere else, the design is leaking.

## Stacks

core (cluster, CNI, metrics-server, Gateway API via Envoy Gateway, cert-manager) · gitops · registry · ci (GitHub Actions runners via ARC, Jenkins optional) · secrets (External Secrets Operator) · obs (kube-prometheus-stack, Loki, Tempo, OTel Collector, Grafana, Sloth) · policy (Kyverno) · scaling (KEDA local, Karpenter cloud) · chaos (Chaos Mesh) · ai (Ollama, Langfuse) · sample-app.

Local memory profiles: `minimal` (~4 GiB), `standard` (~10–12 GiB), `full` (~16–19 GiB). The default is `minimal` plus obs through `extraStacks: [obs]` in `lab.yaml` (4.6 GiB measured), because those are the stacks the local provider runs today; `standard` becomes the default once its stacks exist. Harbor only when a lab needs it.

## TUI rules

- First frame from cached state in under 100 ms; never block the key loop. All work runs as background `tea.Cmd`s that stream messages.
- Watch Kubernetes with client-go informers; never poll `kubectl`.
- Use libraries over CLIs: Helm SDK, kind library, terraform-exec, AWS SDK v2.
- Log panes: ring buffer of 5,000 lines, at most 20 redraws a second.
- k9s-style keys: `1`–`6` screens, `:` palette, `/` filter, `u`/`d` up/down the selected stack (`d` asks first), `U` the whole profile, `r` retry a failed stack, `l` logs, `K` open k9s, `m` mode, `v` verify, `?` help.
- State shown with symbol and colour together: ● healthy, ◐ progressing, ○ off, ✕ failed.
- Every TUI action logs the equivalent CLI command.

## Cloud guardrails

- Every environment has a TTL (default 2h, `lab up --mode cloud --ttl`, never more than 8h ahead, `lab extend` included). A Lambda reaper on an EventBridge schedule (every 15 min) is the dead man's switch: past `expires-at` it deletes controller-made resources (Karpenter nodes, load balancers, target groups, their security groups, volumes) and starts the CodeBuild project `lab-teardown` (arm1.small), which runs `tofu destroy` on the cluster and registry state from the bundles lab uploads before each apply, sweeps again and emails a summary (SNS `lab-teardown`).
- `lab up --mode cloud` refuses unless the reaper is out of dry run and wrote a clean heartbeat (`reaper/heartbeat.json` in the state bucket) in the last 30 minutes, and unless `k8sVersion` is the newest EKS version in standard support (cluster `upgrade_policy` STANDARD). `lab doctor --mode cloud` checks both.
- No load balancer by default: `lab tunnel` port-forwards 127.0.0.1:8080/8443 to the Envoy Gateway Service, so cloud apps keep the `https://<app>.localtest.me:8443` URLs. No NAT, no control-plane logging, no Container Insights, 20 GB gp3 root disks.
- AWS Budgets alerts at 50/80/100%. Spot nodes, no NAT gateway, one shared load balancer, short log retention.
- `lab down` destroys in reverse order, then lists anything still tagged `lab=true` outside the baseline.
- AWS access: CLI profile `lab` assumes the `lab-admin` role with MFA (source profile `devops`, the IAM user). lab shares the AWS CLI's role cache (`~/.aws/cli/cache`, same file names as botocore), so one MFA code per 4-hour session serves lab, tofu, `aws eks get-token` and the aws CLI. `lab cloud login` forces a fresh session. lab passes temporary keys to tofu. Bootstrap the baseline with `--profile devops`.
- Cloud lab (`lab up --mode cloud`, `lab cloud plan`): modules `infra/terraform/aws/cluster` (VPC, EKS, Karpenter IAM, Pod Identity) and `infra/terraform/aws/cluster/registry` (ECR), state under `cluster/` in the state bucket. lab installs Karpenter and the AWS Load Balancer Controller itself (Helm SDK), because they must exist before GitOps and carry expires-at. A cloud lab keeps its own state, kubeconfig and expiry under `~/.lab/cloud/`, beside a local lab; the lab lock is shared. `lab extend 1h` retags everything with expires-at.
- Account baseline (`infra/terraform/aws/baseline`, `lab cloud baseline plan|apply`, OpenTofu via terraform-exec): state bucket `lab-tofu-state-<account>` with `use_lockfile`, `lab-admin`, budget `lab-monthly`, TTL reaper `lab-ttl-reaper`. Baseline resources are permanent: `lab=true` and `owner` but no `expires-at`, which the reaper relies on to keep them. Private inputs (alert email) live in `~/.lab/aws/baseline.tfvars`.

## Repository layout

```text
cmd/lab/                 main.go, Cobra commands
internal/tui/            Bubble Tea models, one per screen
internal/engine/         plan, apply, destroy, dependency order
internal/provider/local  kind, Podman, LocalStack, Zot
internal/provider/aws    Terraform runner, EKS, ECR, cost, TTL
internal/kube/           client-go informers, health, waits (nodes, Argo CD apps)
internal/gitops/         Argo CD install (Helm SDK), app-of-apps, in-cluster stacks
internal/state/          ~/.lab/state.json, ops history
internal/config/         lab.yaml loading and validation
internal/drills/         fault injection and timers
internal/doctor/         host readiness checks for `lab doctor`
infra/terraform/{local,aws}
gitops/{bootstrap,stacks,overlays}
labs/                    lab-NN-*.md + verify scripts
drills/                  drill definitions (YAML)
docs/                    design.md, notes
lab.yaml
Makefile                 builds linux/amd64 and darwin/amd64
```

## Roadmap (12 weeks)

0. Foundations (wk 1): repo skeleton, Cobra CLI, `lab doctor`. Gate: doctor all green.
1. Local core (wk 2–3): engine + local provider; core, gitops, obs, sample-app. Gate: healthy in under 5 min.
2. TUI v1 (wk 4–5): Home, Stacks, Ops log, palette, k9s handoff. Gate: first frame under 100 ms.
3. Cloud mode (wk 6–7): Terraform VPC/EKS/ECR, TTL, reaper, budget, Cost screen. Gate: no leftovers after down.
4. Labs and drills (wk 8–9): 6 labs, 7 drills with timers. Gate: labs pass in both modes.
5. AI and release (wk 10–12): ai stack, AI On-Call Assistant, README, demo, binaries. Gate: v1.0 tag.

If a phase slips, cut TUI screens before labs.

## `lab doctor` checks (phase 0)

Podman rootless mode and version · `podman.socket` (user) · cgroup v2 controllers delegated to the user (cpuset cpu io memory pids) · inotify limits (`max_user_watches` ≥ 524288, `max_user_instances` ≥ 512) · iptables modules (`ip_tables`, `iptable_nat`, `ip6_tables`, `ip6table_nat`) · SELinux mode and `container_use_devices` · NVIDIA module loaded, `nvidia-smi`, free VRAM · `nvidia-ctk cdi list` shows `nvidia.com/gpu=all` · Ollama answers on 11434 · tool versions (go, kind, kubectl, helm, k9s, tofu/terraform, aws) · free RAM against the chosen profile. Output: coloured table, `--json` flag, non-zero exit on failure.

## Conventions

- Go: standard layout, `golangci-lint`, table-driven tests, no global state outside `cmd/`.
- Commits: small, conventional (`feat:`, `fix:`, `docs:`, `chore:`).
- Never push to `main`; push a branch and open a PR. `.githooks/pre-push` refuses pushes to `refs/heads/main`, and `.claude/settings.json` denies `git push origin main` and `git push -f`/`--force`. **Every new clone must run `git config core.hooksPath .githooks` once**, or the hook does nothing.
- Writing: plain, professional, short sentences. No marketing tone.
