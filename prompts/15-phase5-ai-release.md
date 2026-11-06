# 15 — Phase 5: AI On-Call Assistant and v1.0

Needs: the phase 4 gate passed. Gate: the v1.0 tag. Send these as separate PRs.

## 15a — ai stack

```text
ai stack.
- Local: use the host's Ollama (Quadlet user service, 127.0.0.1:11434,
  GTX 1650 with about 3.6 GiB usable VRAM). Don't run a second Ollama in
  the cluster. Reach it from kind through the host address, never the
  LAN; it has no auth, so it stays closed in firewalld. Models must fit
  the VRAM: 1B–3B, 4-bit (llama3.2:3b is the reference).
- Langfuse (pinned) for LLM traces and evals, with its database on a
  small PVC; secrets through ESO.
- Cloud: a small model on a GPU spot node via Karpenter, only when a lab
  or the assistant asks for it, with a hard cap and the normal TTL.
  Check GPU spot prices in ap-south-1 first and give me the cost.
- compat rows, lab doctor checks (Ollama answers, model pulled, VRAM
  free), memory measured. Profile full includes ai.
```

## 15b — AI On-Call Assistant

```text
AI On-Call Assistant: given an alert, it gathers context and proposes
a diagnosis and next steps, with every claim tied to evidence.
- Input: an Alertmanager webhook, or `lab assist <alert|drill>`.
- Tools it may use, read-only: Kubernetes API (events, pod status, logs
  through Loki), Prometheus queries, Argo CD app history. No write
  actions, no shell.
- Output: a short incident note (what fired, what it found, likely
  cause, the commands to confirm, the fix) and a TUI pane.
- Trace every run in Langfuse.
- Evals: run it against all 7 drills; the drill's `cause` is ground
  truth. Score cause correctness, evidence quality and time. Seed the
  eval set with known model mistakes (for example "a failed liveness
  probe replaces the container" is wrong: the kubelet restarts it in the
  same pod).
- Works with the local small model and, as an option, a hosted model;
  never sends cluster data anywhere without a flag the user sets.
```

## 15c — README, demo, release

```text
Release v1.0.
- Root README: what it is, a screenshot/GIF of the TUI, quick start for
  local mode, cloud mode with the cost rules, labs and drills tables,
  architecture diagram, link to docs/design.md.
- Demo: a short recorded walkthrough (lab up, a drill with the
  assistant, lab down).
- Release binaries (linux/amd64, darwin/amd64) with checksums from CI
  on a tag; `make install` documented.
- Final public-readiness check: no company names, no secrets in history
  (gitleaks over the whole history), licences of every pinned
  component listed.
- Tag v1.0.0.
```
