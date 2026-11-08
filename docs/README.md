# Documentation

Start with the local guide. Use cloud mode only for an approved AWS session.
The repository is private; you need access to clone it.

## I want to...

| I want to... | Read |
| --- | --- |
| Run the lab on a Linux host | [Local getting started](getting-started/local.md) |
| Run an approved AWS session and clean up | [Cloud getting started](getting-started/cloud.md) |
| Understand the CLI, TUI and GitOps | [Architecture](concepts/architecture.md) |
| Read a failed readiness check | [The doctor reading guide](getting-started/local.md#read-lab-doctor) |
| Take a first-hour tour without injecting faults | [The local tour](getting-started/local.md#first-hour-tour) |
| Use every TUI screen and key | [TUI guide](guides/tui.md) |
| Find exact CLI arguments and flags | [Generated CLI reference](guides/cli.md) |
| Choose a profile or add/remove a stack | [Profiles and stacks](guides/profiles-and-stacks.md) |
| Investigate with the local assistant and understand its limits | [Assistant guide](guides/assistant.md) |
| Choose, work through and clean up a hands-on exercise | [Labs guide](guides/labs.md) |
| Practise an incident from injection through recovery | [Drills guide](guides/drills.md) |
| Diagnose a known host or deployment problem | [Troubleshooting](guides/troubleshooting.md) |
| Verify a downloaded binary | [Release binaries](#release-binaries) |
| Check component and model terms | [Licence inventory](licences.md) |
| Change the implementation | [Engineering reference](design.md) |

The task and exercise guides are available above. Individual lab and drill
pages use a consistent lifecycle layout. Reference solutions are labelled
spoilers, not required reading. Offline link-check CI and the full documented
read-only command accuracy pass follow in PR 4.

## Safety labels

**Read-only** means no infrastructure is changed. A cloud query still contacts
AWS; resources already running continue to bill.

**Changes the lab** means the command creates or changes local resources.
**Destructive** means it deletes resources or data.

**Billed — approval required** means AWS resources can incur charges.
Review the account, plan, duration and cost before approving an apply or
destroy. A budget and a TTL are safeguards, not hard spending caps.

Never run Docker. Use rootless Podman. Keep application listeners on loopback.
Do not copy credentials, private keys, kubeconfigs or state into Git.

## Release binaries

[v1.0.0](https://github.com/chanupen/devops-lab/releases/tag/v1.0.0) was released
on 2026-11-08. Download your platform's binary and `SHA256SUMS` from the same
release. Also retain its `LICENSE` and `THIRD_PARTY_NOTICES.txt`.
Run the following in the download directory before executing the binary.

Linux:

```sh
sha256sum --check --ignore-missing SHA256SUMS
```

**What you will see:** `lab-linux-amd64: OK`. Other downloaded assets also show
`OK`. Missing checksummed files are skipped; an empty or mismatched download
does not pass.

Intel macOS:

```sh
grep '  lab-darwin-amd64$' SHA256SUMS | shasum -a 256 --check
```

**What you will see:** `lab-darwin-amd64: OK`, or a failure. Stop on failure.
A checksum detects corruption; it is not an independent signature when both
downloads come from the same source.

The local guide uses source installation with `make install`. The macOS
binary is a client, not a local Kubernetes workload host.

## Engineering reference

[design.md](design.md) remains the engineering reference for implementation,
compatibility decisions and measurements. The getting-started guides contain
the steps needed to run and remove a lab; they do not require reading it first.
