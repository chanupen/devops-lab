# Security policy

## Report a vulnerability

Report vulnerabilities privately. Do not open a public issue.

Use GitHub's private reporting: on the repository's **Security** tab, choose
**Report a vulnerability**. This opens a private security advisory that only
the maintainers can see.

Include the affected version or commit, the steps to reproduce, and the
impact you expect. Do not include real credentials, keys or personal data.

## Scope

In scope:

- The `lab` binary and its source (`cmd/`, `internal/`).
- Scripts in the repository: labs, drills, release and CI helpers.
- Infrastructure as code: OpenTofu modules under `infra/` and GitOps
  manifests under `gitops/`.

Out of scope: vulnerabilities in third-party software the lab installs
(report those upstream), and lab environments you run yourself.

## Bug bounty

There is no bug bounty. Reports are handled on a best-effort basis.
