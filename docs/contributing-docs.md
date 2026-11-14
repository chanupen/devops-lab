# Contribute documentation

[Docs index](README.md) · [Engineering reference](design.md)

Reader pages explain how to use the lab without reading design.md.
The design remains the engineering reference. Keep both accurate when
behavior changes. The repository is public under the MIT licence.

## Writing

Use short, plain sentences. Prefer the exact command, a concrete result and
a brief explanation over a long introduction. Avoid marketing language.
Keep names, examples and data generic. Never include credentials, kubeconfigs,
private keys, state, identifying terminal titles or secret command output.

After every shell command block, add **What you will see:**. Say whether the
command prints a table, prompts, writes a file or normally prints nothing.
Show failures and prerequisites too; do not describe a healthy example as a
guarantee. Counts, timing, versions and host capacity can vary.
Generated CLI help blocks are transcripts, not shell examples.

Before a command, distinguish **Read-only**, **Changes the lab** and
**Destructive**. For billed provisioning and cleanup, use
**Billed — approval required**. Label AWS read-only queries as cloud work;
do not run them when an accuracy pass forbids cloud work. They do not stop
existing resource billing. TTL and budgets are not hard spending caps.
Keep loopback binding, rootless Podman, ownership tags and teardown checks explicit.

## Exercises and spoilers

Drill pages describe the incident and evidence, not the actual injected cause
or a repair recipe. Full answers belong in labelled solution scripts.
Warn that the last runtime hint and completed/aborted results can reveal the cause.
Use blind/random practice when catalog names themselves would give clues.

Every exercise page has Goal, Prerequisites, Scenario/Steps, What you should see,
Hints, How to verify and Cleanup. Preserve the runtime Markdown contract:

- Drill incident text is before the first H2.
- Lab lifecycle sections are before the first `## Step N: title`.
- Lab steps retain `### Hints` and `### Expected result`.

Do not insert global cleanup below the final step: the parser would treat it
as expected-result text. Keep existing step numbers/titles unless the exercise
changes deliberately. Review synthetic TUI golden diffs when runtime text changes.
Do not run exercises on the live lab just to validate documentation.

## Links and diagrams

Use relative repository links and GitHub-style heading fragments.
The offline checker covers Markdown links/images, reference links, HTML
href/src links and explicit anchors, duplicate heading anchors, and line ranges.
It checks destinations stay in the repository and exist. External URLs are
skipped, not fetched or proved live. Fenced and inline code examples are not
treated as links.

Mermaid uses a deliberately small offline flowchart subset:
`flowchart` or `graph`, then `LR`, `RL`, `TD`, `TB` or `BT`.
Declare each node with `ID["label"]`; reference the ID in subsequent edges.
Supported arrows are `-->`, `<--`, `<-->`, `---`, `==>`, `-.->` and
`-. label .->`; ordinary arrows can have `|label|`.
One line can chain edges. Full-line `%% ` comments and
`click ID "relative/path.md#anchor"` links are supported.
The checker rejects missing nodes, malformed arrows, unclosed fences and
unsupported syntax rather than pretending to render it.
It does not run JavaScript or a browser. A new diagram type needs an explicit
offline parser/test extension, not a CDN dependency.

## Validate an edit

**Offline; no lab, network or credentials required.** Python 3.10+ and make
are enough for the link/diagram checker and its regression tests.

```sh
make docs-check
```

**What you will see:** regression test results and a page/link/Mermaid count,
or source locations for failures. CI runs the same target.

For CLI text, regenerate/check the help reference using a Go toolchain:

```sh
python3 scripts/generate-cli-docs.py --check
```

**What you will see:** `CLI documentation is current`, or a diff and non-zero exit.
The script builds a temporary binary and runs help only, not command actions.

```sh
make lint test
```

**What you will see:** linter results and race-enabled tests for both Go modules.
`make test` also runs the offline documentation checks.
These are not live exercise e2e tests.

For an accuracy pass, run permitted local read-only reports on the workload
host and compare their output. Check help/code for destructive commands and
cloud commands without executing them. Hints, solution display, drill status
and assistant commands can record state or telemetry even when they do not
change Kubernetes; inspect the implementation before labelling them read-only.
Keep machine-specific audit logs outside Git and report limitations honestly.
