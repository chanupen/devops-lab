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
- MacBook Pro (Intel, `macbookprosys`): client only. Reaches the TUI with `ssh -t fedora lab`.

## Architecture

- `lab` TUI (Bubble Tea + Bubbles + Lip Gloss) → engine (plan, apply, destroy, dependency order, state in `~/.lab/state.json`) → providers.
- Providers implement one Go interface: `Cluster()`, `Registry()`, `Secrets()`, `ObjectStore()`, `Cost()`, `Destroy()`.
  - `local`: kind on rootless Podman, LocalStack, Zot registry, Ollama on the host.
  - `aws`: Terraform via terraform-exec, AWS SDK v2, EKS + Karpenter (spot), ECR, Secrets Manager, EKS Pod Identity.
- After bootstrap, GitOps owns everything: **Argo CD by default, Flux as the second option** (`gitops: argocd | flux`). Never run both except in the comparison lab.
- Mode differences live only in `gitops/overlays/` and `internal/provider/`. If supporting one mode needs changes anywhere else, the design is leaking.

## Stacks

core (cluster, CNI, metrics-server, Gateway API via Envoy Gateway, cert-manager) · gitops · registry · ci (GitHub Actions runners via ARC, Jenkins optional) · secrets (External Secrets Operator) · obs (kube-prometheus-stack, Loki, Tempo, OTel Collector, Grafana, Sloth) · policy (Kyverno) · scaling (KEDA local, Karpenter cloud) · chaos (Chaos Mesh) · ai (Ollama, Langfuse) · sample-app.

Local memory profiles: `minimal` (~4 GiB), `standard` default (~10–12 GiB), `full` (~16–19 GiB). Harbor only when a lab needs it.

## TUI rules

- First frame from cached state in under 100 ms; never block the key loop. All work runs as background `tea.Cmd`s that stream messages.
- Watch Kubernetes with client-go informers; never poll `kubectl`.
- Use libraries over CLIs: Helm SDK, kind library, terraform-exec, AWS SDK v2.
- Log panes: ring buffer of 5,000 lines, at most 20 redraws a second.
- k9s-style keys: `1`–`6` screens, `:` palette, `/` filter, `u`/`d` up/down, `l` logs, `K` open k9s, `m` mode, `v` verify, `?` help.
- State shown with symbol and colour together: ● healthy, ◐ progressing, ○ off, ✕ failed.
- Every TUI action logs the equivalent CLI command.

## Cloud guardrails

- Every environment has a TTL (default 4h). A Lambda reaper on an EventBridge schedule (every 15 min) destroys anything past `expires-at`.
- AWS Budgets alerts at 50/80/100%. Spot nodes, no NAT gateway, one shared load balancer, short log retention.
- `lab down` destroys in reverse order, then lists anything still billed (by tag).
- AWS access today is IAM user `devops` (CLI profile `lab`); phase 3 moves to an MFA-assumed `lab-admin` role.

## Repository layout

```text
cmd/lab/                 main.go, Cobra commands
internal/tui/            Bubble Tea models, one per screen
internal/engine/         plan, apply, destroy, dependency order
internal/provider/local  kind, Podman, LocalStack, Zot
internal/provider/aws    Terraform runner, EKS, ECR, cost, TTL
internal/kube/           client-go informers, health
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
- Writing: plain, professional, short sentences. No marketing tone.
