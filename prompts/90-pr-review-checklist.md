# 90 — PR review checklist

Use this before merging any PR. You can also give it to a second agent and ask it to review the PR against it.

## Every PR

- CI green on the head commit (test, build, lint), no conflicts.
- The PR does what the prompt asked, and nothing hidden. Changes outside `gitops/overlays/` and `internal/provider/` are explained.
- No secrets, kubeconfigs, state files or binaries in the diff. Check the file list.
- Versions pinned (charts, images by digest, tools with checksums), each in standard support, each with a compat row if it limits Kubernetes.
- Docs updated in the same PR: `docs/design.md`, `CLAUDE.md`, `labs/README.md` or `drills/README.md` as relevant.
- Evidence in the description: test output, timings, memory added, what was run on the host. "Tests pass" with no output isn't evidence.
- Squash and merge when the branch has temporary or failing intermediate commits.

## Code that touches the cluster or the host

- Nothing waits forever: every wait has a timeout and a clear failure message.
- Failures restore or clean up, and say what to run next.
- Errors are not swallowed (`return nil` on an error that means "unknown" is a bug: fail closed).
- Rootless Podman only; no sudo; nothing written outside the repo or `~/.lab` without asking.

## Labs

- Verify scripts are read-only and fail before their solution.
- `reset.sh` removes only the lab's namespaces and labelled objects.
- Teaches current APIs (no deprecated kinds) and real-world practice, with the shortcuts named as shortcuts.
- e2e passed on the head commit, plus a learner pass.

## Drills

- verify never names the cause before recovery (spoilers check in e2e).
- Inject + abort leaves everything exactly as before: compare objects, stopped containers, netem rules, Moto and Secrets.
- The fault goes through Argo CD, never Git.
- Test runs stay out of the learner's history.

## Cloud

- A baseline plan destroys or replaces nothing, and leaves the budget, schedule and SNS alone.
- Every new resource is tagged `lab=true`, `owner`, `expires-at`, and the reaper and `lab down` know how to delete it.
- The leftovers report after `lab down` is in the PR.
- Every AWS managed policy ARN exists (checked before apply).
