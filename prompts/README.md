# Build prompts

These prompts rebuild `devops-lab` from an empty repo, or continue it from where it stands. Each one fits a single PR. They work with any coding agent (Claude Code, Codex, Cursor, Aider and others), so long as it can run commands on the Fedora host.

## How to use them

1. Give the agent `00-agent-rules.md` first. If the agent reads `AGENTS.md` or `CLAUDE.md` on its own, keep the repo's `CLAUDE.md`; it holds the same rules. Copy it to `AGENTS.md` for agents that look for that name.
2. Send one prompt per PR, in order. Each prompt lists what it needs before it can start.
3. Review every PR with `90-pr-review-checklist.md` before you merge it.
4. When something breaks on the host, look in `91-known-issues.md` first.
5. Cloud work always goes through `13-cloud-session.md`. Never let an agent run a cloud `lab up`, `tofu apply` or `destroy` without your approval.

`docs/design.md` is the source of truth for the design. The prompts say what to build and in what order; the design doc says how it works. When the two disagree, the design doc wins, and the prompt gets fixed.

## Order and status

| File | Phase | Builds | Status (7 Nov 2026) |
| --- | --- | --- | --- |
| `01-phase0-foundations.md` | 0 | Repo skeleton, Cobra CLI, `lab doctor` | Done (#1, #4) |
| `02-phase1-local-core.md` | 1 | Engine, state, local provider, gitops, core, obs, sample-app | Done (#2–#7) |
| `03-phase2-tui.md` | 2 | TUI v1: screens, actions, palette, k9s handoff | Done (#8–#10) |
| `04-phase3a-cloud-baseline.md` | 3 | Account baseline: state bucket, `lab-admin` with MFA, budget, reaper, teardown | Done (#12–#16) |
| `05-phase3b-cloud-cluster.md` | 3 | EKS, Karpenter, ECR, TTL, `lab extend`, `lab tunnel`, leftovers | Done (#12–#16) |
| `06-hardening-local.md` | 3–4 | Stopped containers after a reboot, version gate, `make install`, CI speed | Done (#18–#23, #25) |
| `07-lab1-framework.md` | 4 | Labs framework and Lab 1 | Done (#17, #18) |
| `08-labs-2-3-5.md` | 4 | Labs 2, 3, 5, the lab registry, the policy stack | Done (#24) |
| `09-lab4-secrets.md` | 4 | Lab 4, the secrets and sample-db stacks, Moto | Done (#26) |
| `10-drills-1.md` | 4 | Drill framework, bad-deploy, oom, dns | Done (#27) |
| `11-drills-2.md` | 4 | cert-expiry, node-loss, slow-db, secret-drift | Done (#28) |
| `12-cloud-overlays.md` | 4 | Cloud GitOps overlays for every stack | Open: PR #29, needs one passing cloud session |
| `13-cloud-session.md` | — | How to run any cloud session safely | Use every time |
| `14-lab6-and-gate.md` | 4 | Lab 6 (Karpenter) and the phase 4 gate | Next |
| `15-phase5-ai-release.md` | 5 | ai stack, AI On-Call Assistant, README, demo, v1.0 | 15b tools/runner/webhook, eval (#40) and TUI (#41) done; 15c public-readiness audit in this draft PR ([findings](../docs/public-readiness.md)); repository licence decision pending; README/demo and release pipeline not started |

## To continue from today

1. `13-cloud-session.md` with the session 5 plan, on `feat/cloud-overlays`. Then merge PR #29.
2. `14-lab6-and-gate.md`.
3. `15-phase5-ai-release.md`.

## To rebuild from nothing

Run 01 to 15 in order. The prompts for finished work already include the fixes that came later, so a rebuild avoids the bugs found the first time. The lessons behind them are in `91-known-issues.md`.
