# Public-readiness audit

> **14 Nov 2026:** the repository was made public under the MIT licence.
> Secret scanning, push protection, Dependabot alerts, private vulnerability
> reporting and the `main` ruleset (no force push, no deletion, changes
> through pull requests only) are enabled. The audit below is kept as a
> record.

7 Nov 2026. Baseline: `f29cb38` on `main`. This is release PR 1 only.
It is not a declaration that the repository is ready to publish.

## Secret scan

Gitleaks 8.30.1 was downloaded from its official release and checked against
the published SHA256SUMS. It ran locally, with redacted reports. Nothing was
sent to a hosted scanner.

All remote branch heads, tags and PR heads were fetched. The repository is
not shallow. The scan covered **282 reachable commits**, including each
merge parent's patch, with no repository allowlist:

```sh
git fetch origin '+refs/heads/*:refs/remotes/origin/*' \
  'refs/pull/*/head:refs/audit/pull/*/head' --tags
# defaults.toml contains only: [extend] and useDefault = true
gitleaks git . --log-opts='--all --full-history -m' \
  --config defaults.toml --redact=100 --report-format json --report-path history.json
gitleaks dir . --config defaults.toml --redact=100 \
  --report-format json --report-path working-tree.json
```

**Result: zero gitleaks findings in history and the working tree.**
The repository's Moto dummy-key allowlist was not used. No history was
rewritten, credentials rotated, lab operation run or cloud API called.
No tracked kubeconfig, Terraform state, PEM or private-key filename was found
in the historical file-name check. A pattern scan cannot prove there are no
secrets, encrypted private data or identifying text.

## Identifying text

The text scan examined **1,717 distinct blob/path versions** (1,697 blobs)
and commit messages across those 282 commits. Author headers were excluded;
co-author trailers were reviewed. Binary blobs were not interpreted as text.

The raw hit list is kept privately. It lists every grouped match, all
observed historical locations and baseline-main line numbers.
The key is category, literal and file; repeated appearances across revisions
are grouped, not omitted. Private identifying literals are redacted in the
report to avoid republishing them. The cited original commits remain readable.
The baseline line numbers precede this PR's edits.

| Category | Grouped matches | Review |
| --- | ---: | --- |
| Company/vendor terms | 433 | Public upstream vendors, software, domains and reference hardware; `hcl` is a language/extension false positive. No private employer or proprietary business data observed in the reviewed matches. |
| Hostname/domain candidates | 914 | Upstream domains, Kubernetes API groups, generic lab/service/test names and syntax false positives; two personal machine names were generalized. |
| Email-shaped strings | 15 | Eight distinct strings; all generic examples, SSH addresses, a systemd unit or automated co-author addresses. No personal email beyond commit authors observed. |
| AWS-account-shaped numbers | 17 | Three synthetic test accounts; two other numeric strings are a build timestamp and a Go pseudo-version suffix. |
| Kubeconfig/tfstate path expressions | 33 | Generic operational, temporary and ignored-file paths; no credential or state contents found. |
| Personal attribution | 1 | Removed a personal design-document byline; original authorship stays in Git history. |

The company search used a finite list of common employer/vendor names, then
reviewed the domain matches for other organizations. It is not an exhaustive
entity-recognition or legal review. Public product names and upstream licence
attributions are not private employer data and were not stripped.

### Fixes at the tip

| Baseline location | Change |
| --- | --- |
| `CLAUDE.md:17` | Personal workload-host name replaced with example `lab-host`. |
| `CLAUDE.md:21` | Personal client-host name replaced with example `lab-client`. |
| `docs/design.md:119,600` | SSH and configuration examples use `lab-host`. |
| `docs/design.md:5` | Personal byline removed from the publication document. |

These are documentation-only substitutions. No SSH configuration, cloud
ownership tag, provider code, cluster state or live workload was changed.
Both old host names and the byline **remain in history**. On 7 Nov 2026 the
maintainer accepted retaining historical host names: hostnames only, no
secrets and no history rewrite. This decision does not waive secret checks
or third-party redistribution terms. Original authorship is unchanged.

### Retained matches

The synthetic accounts are `111111111111`, `111122223333` and `123456789012`,
only in test fixtures. `202601010000` is `Makefile:42`'s fixed archive timestamp;
`605354379745` is part of a Go pseudo-version in `go.sum`, not an account.

The email-shaped strings are `admin@lab.local`, `t@example.com`,
`you@example.com`, `git@github.com`, `user@github.com`, `user@1000.service`,
and the Copilot and Claude automated noreply co-author addresses. The index
lists every file or commit-message occurrence. The Langfuse login is generic;
its password comes from a Secret, not that address.

Generic paths such as `~/.lab/kubeconfig`, `~/.lab/labs/<id>/kubeconfig`,
`~/.kube/config`, and `terraform.tfstate` describe where private runtime files
belong. They are not leaked runtime files. Keep them out of Git.

## Licences and maintainer decisions

[licences.md](licences.md) accounts for all 388 module/version rows selected
by the root and sample-db manifests, primary and bundled charts, rendered and
explicit image pins, and the local model's separate terms. It distinguishes
project licences from the other software packaged in container images.

Before publication or release:

1. **Repository licence: decided.** The maintainer approved [MIT](../LICENSE),
   copyright 2026 chanupen, on 7 Nov 2026. Third-party licences remain separate.
2. **Historical host names: accepted.** Hostnames stay in history; no secrets
   may be retained and no history rewrite is authorized. Host examples are
   generic at the tip. Original authorship remains unchanged.
3. **Review redistribution terms.** MPL-covered Go code needs source/notices;
   Grafana/Loki and Redis have copyleft or source-available terms; Langfuse
   includes commercial EE portions; model weights have their own agreements.
   Qwen2.5 3B's research licence is non-commercial, not Apache-2.0.
4. **Resolve inventory caveats before bundling artifacts.** The inventory is
   not a package-by-package image SBOM or a binary notice bundle. One Go
   dependency declares MIT only in its README. Preserve upstream notices and
   review the exact image/model artifacts and any modified covered code.
5. **Keep or generalize deployment ownership separately.** The `chanupen`
   namespace and owner tag bind this lab's cleanup safeguards. They are not
   secrets. This PR deliberately does not change them or the live lab.

README/demo and release-pipeline PRs have not started. No v1.0.0 tag was made.
