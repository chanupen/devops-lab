# 01 — Phase 0: foundations

Needs: an empty private repo, Go installed on Fedora, `00-agent-rules.md` sent. Gate: `lab doctor` all green on the Fedora host.

```text
Phase 0: repo skeleton, Cobra CLI and `lab doctor`.
Worktree: git worktree add ../devops-lab-wt-p0 -b feat/foundations origin/main

1. Repo skeleton, exactly this layout (empty packages get a doc.go):
   cmd/lab/ internal/{tui,engine,provider/local,provider/aws,kube,gitops,
   state,config,labs,drills,doctor}/ infra/terraform/{local,aws}/
   gitops/{bootstrap,stacks,overlays}/ labs/ drills/ docs/ lab.yaml
   Makefile. CLAUDE.md (copied from prompts/00-agent-rules.md plus the
   layout) and AGENTS.md (a symlink to it). .gitignore covers bin/, /lab,
   *.tfstate*, .terraform/, kubeconfig*, *.pem, .env.
2. Makefile: build (CGO_ENABLED=0, -s -w, -X main.version from git
   describe), build-all (linux/amd64, darwin/amd64), test (go test
   -race), lint (golangci-lint, pinned version), install (to
   BINDIR ?= ~/.local/bin, no sudo; warn if BINDIR isn't on PATH or an
   older lab shadows it), uninstall, help. `lab version` prints the
   version.
3. Cobra CLI with the commands stubbed and documented: doctor, plan, up,
   down, status, extend, tunnel, labs, drills, cloud, version. `--config`
   flag; without it read ./lab.yaml, then ~/.lab/lab.yaml.
4. internal/config: load and validate lab.yaml (mode local|cloud,
   profile minimal|standard|full, extraStacks, gitops argocd|flux,
   k8sVersion, repo.url + repo.sshKeyFile, cloud.region/ttl/owner).
   Clear errors with the field name. Table-driven tests.
5. `lab doctor` (internal/doctor), each check its own function with a
   fix line:
   - Podman rootless mode and version; podman.socket (user) active
   - cgroup v2 controllers delegated to the user: cpuset cpu io memory pids
   - inotify: max_user_watches >= 524288, max_user_instances >= 512
   - iptables modules: ip_tables iptable_nat ip6_tables ip6table_nat
   - SELinux mode and container_use_devices
   - NVIDIA module loaded, nvidia-smi, free VRAM; nvidia-ctk cdi list
     shows nvidia.com/gpu=all
   - Ollama answers on 127.0.0.1:11434
   - tool versions: go kind kubectl helm k9s tofu aws
   - free RAM against the profile
   Output: a coloured table with symbol and colour together
   (● ok, ◐ warn, ✕ fail, ○ skipped), `--json`, non-zero exit on any
   failure. Checks run in parallel with a timeout each.
6. CI (.github/workflows/ci.yml): test, build, lint jobs on PRs and
   pushes to main. Actions pinned by full SHA. Per-job Go caches
   (actions/cache with github.job in the key, saved only on main).
   PRs build linux/amd64 only; main builds all targets. concurrency
   cancels superseded PR runs.
7. .githooks/pre-push refuses pushes to refs/heads/main. Document
   `git config core.hooksPath .githooks` in CLAUDE.md.
8. docs/design.md: copy in the design (goals, modes, architecture,
   stacks, TUI, labs, drills, cloud guardrails, roadmap).

Evidence in the PR: `lab doctor` output from the Fedora host.
```
