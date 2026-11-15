# devops-lab instructions

`lab` is a Go CLI and Bubble Tea TUI for a DevOps/SRE home lab. It runs the
same platform locally or in AWS. Read `docs/design.md` before changing behavior;
keep it updated in the same PR when the design changes. `CLAUDE.md` contains
the complete operational constraints.

## Architecture

- The CLI and TUI share `internal/engine`; do not implement separate behavior.
  The engine plans dependency order and persists state; providers perform
  mode-specific work.
- Keep mode-specific behavior in `internal/provider/` and `gitops/overlays/`.
  A change requiring local/cloud branching elsewhere indicates a design leak.
- After bootstrap, GitOps owns workloads. Argo CD is the default; Flux is the
  alternative. Do not run both outside the comparison lab.
- Preserve the shared lab lock: mutating operations must not run concurrently;
  read-only views must remain non-blocking.

## Non-negotiable safety rules

- Use rootless Podman only. Never install or invoke Docker. kind uses
  `KIND_EXPERIMENTAL_PROVIDER=podman`.
- Never commit secrets, AWS credentials, kubeconfigs, or Terraform/OpenTofu
  state.
- Ask before host-destructive actions, `sudo` changes, Podman volume removal,
  or a cloud `terraform`/`tofu apply` or `destroy`.
- Cloud resources stay in `ap-south-1` and expiring resources require
  `lab=true`, `owner=chanupen`, and an RFC 3339 `expires-at` tag.
- Keep the Gateway on loopback only; do not expose lab services on the LAN.
- Preserve TTL/reaper safeguards, teardown bundles, and the cloud cleanup
  checks. Fail toward cleanup rather than leaving cloud resources behind.

## Implementation conventions

- Go uses the standard project layout, table-driven tests, and no global state
  outside `cmd/`.
- Prefer Go libraries over shelling out: Helm SDK, kind library,
  terraform-exec, and AWS SDK v2. Kubernetes watches use client-go informers,
  never `kubectl` polling.
- TUI work must not block the key loop. Start from cached state, run work in
  background commands, and retain the log ring-buffer/redraw limits.
- Provider-unsupported stacks are skipped during planning, not failed at run
  time.
- Keep examples, names, code, and data generic. The repository is public.
- Write plain, concise documentation. Keep commits small and conventional.
  Never push directly to `main`.

## Validation

- Run focused tests for the changed package first; run `make test` when the
  change spans packages or affects shared behavior.
- Run `make lint` for Go changes when available.
- GitOps overlay changes must retain local/cloud rendering checks. Labs and
  drills require their documented e2e commands when their behavior changes.
