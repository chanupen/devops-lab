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
- Scripts in the repository, including drills (`drills/`) and labs (`labs/`).
- GitOps manifests under `gitops/`.
- OpenTofu/Terraform modules under `infra/`.
- The TTL reaper Lambda (`cmd/reaper`) and its teardown build.
- The CI and release workflows under `.github/workflows/`.

Out of scope: bugs in upstream tools the lab installs or uses, such as kind,
Argo CD or Karpenter. Report those to the upstream project.

## Bug bounty

There is no bug bounty. Reports are handled on a best-effort basis.

## Cloud costs

Cloud mode creates billable AWS resources. You run it at your own cost.
