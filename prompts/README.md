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

| File | Phase | Builds | Status (8 Nov 2026) |
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
| `12-cloud-overlays.md` | 4 | Cloud GitOps overlays for every stack | Done (#29, passing session #32) |
| `13-cloud-session.md` | — | How to run any cloud session safely | Use every time |
| `14-lab6-and-gate.md` | 4 | Lab 6 (Karpenter) and the phase 4 gate | Done (gate passed 6 Nov 2026; [design](../docs/design.md#build-roadmap)) |
| `15-phase5-ai-release.md` | 5 | ai stack, AI On-Call Assistant, README, demo, v1.0 | Done: [v1.0.0 released](https://github.com/chanupen/devops-lab/releases/tag/v1.0.0) on 2026-11-08; [release workflow](../.github/workflows/release.yml) #45 and [demo](../README.md#demo) #46 merged; all four assets verified against [SHA256SUMS](https://github.com/chanupen/devops-lab/releases/download/v1.0.0/SHA256SUMS) |

## Current release

v1.0.0 was released on 2026-11-08. The repository remains private.
All four downloaded release assets passed `sha256sum --check SHA256SUMS`:

```text
ef22df3d083f66ded69d086c43e42eeb35c44320b04e49d1733a1eb717642447  lab-linux-amd64
cccf5d698caf7990b76fd0073404f0e3ce62835112cec082e7c8f53f906be1ea  lab-darwin-amd64
47645d336159ac26585408aae879dbc64ed4fe0a1b6a0488dcdf768fcd190e71  LICENSE
bad535c73e2b4440012f1d61338836c47fd45f1c3d1b536151dc7fb70d2283a0  THIRD_PARTY_NOTICES.txt
```

## Reader documentation rollout

The [documentation index](../docs/README.md) is the reader entry point.
These four small PRs are separate from the completed release. Each starts
as a draft, with review before the next PR; none is merged by the agent.

| PR | Scope | Status |
| --- | --- | --- |
| 1 | Index, local/cloud getting started, architecture, shorter root README | Approved and merged (#48) |
| 2 | TUI, generated CLI help with drift CI, profiles/stacks, assistant, troubleshooting | Approved and merged (#49) |
| 3 | Drill/lab guides and consistent exercise READMEs, with labelled solutions | Approved and merged (#50) |
| 4 | Offline link-check CI, docs style note, documented read-only command accuracy pass | Approved and merged (#51); `make docs-check` and [contribution rules](../docs/contributing-docs.md) added |

## Guided-use rollout

The [guided design](../docs/design-guided.md) is approved and merged (#52).
Implementation is split into three reviewed PRs; none is merged by the agent.

| PR | Scope | Status |
| --- | --- | --- |
| B1 | Local start/next, shared doctor memory, observational registry probe, fixture goldens and guides | Draft for review; state remains version 1 |
| B2 | Durable lab/drill completion, best times, migration and cached Home summary | Deferred until B1 is merged |
| B3 | Print-only `doctor --fix`, exact commands and sudo labels | Deferred until B1 is merged and owner authorises it; not implemented |

## To rebuild from nothing

Run 01 to 15 in order. The prompts for finished work already include the fixes that came later, so a rebuild avoids the bugs found the first time. The lessons behind them are in `91-known-issues.md`.
