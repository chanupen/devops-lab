# Changelog

Notable changes per release. Binaries and `SHA256SUMS` are on each
[GitHub release](https://github.com/chanupen/devops-lab/releases).

## Unreleased

### Docs

- The repository is public: removed statements that it is private, added
  `SECURITY.md` (private vulnerability reporting) and weekly Dependabot
  updates for Go modules and GitHub Actions. Release downloads use anonymous
  `curl` instead of `gh` (#61).

## v1.1.0 (2026-11-12)

### Added

- Guided local use: `lab start` checks the host, picks a profile that fits
  free memory and brings the lab up; `lab next` says what to do next (#53).
- `lab start` and `lab next` detect stopped lab containers after a reboot
  and print the restart command, without hiding host failures (#56).
- Lab and drill completion and best times survive resets and are shown on
  Home; state version 1 is migrated in memory to version 2 (#55).
- `lab doctor --fix` prints the exact fix commands, marked for sudo or
  approval, as text or JSON. It never runs them (#57).
- Reader documentation: docs index, local and cloud getting started,
  architecture, TUI, generated CLI reference, profiles and stacks,
  assistant, troubleshooting, lab and drill guides, offline link checks
  (#48–#52).

### Changed

- `lab next` ignores doctor warnings the user has accepted (#54).
- `lab doctor` reads `profile` and `extraStacks` from `lab.yaml` (or
  `--config`); `--profile` overrides both (#58).
- `lab doctor`'s memory threshold adds each extra stack's measured memory
  to the profile's figure: `minimal + ai` is checked against about
  5.7 GiB, not 4 (#59).

### Fixed

- `gitops: flux` was accepted and the gitops stack silently skipped.
  `lab up`, `lab plan`, `lab start`, `lab next` and `lab doctor` now refuse
  it: "gitops: flux is planned, not built; use argocd" (#58).
- CLAUDE.md, the design doc and reader docs match the code: real engine
  interfaces, and ci, scaling, chaos, Tempo, Sloth (as a stack), Flux and
  the Cost screen marked planned, not built (#58).

## v1.0.0 (2026-11-08)

First release: local and cloud modes, the TUI, six labs, seven drills, the
ai stack and the AI On-Call Assistant. See
[the release](https://github.com/chanupen/devops-lab/releases/tag/v1.0.0).
