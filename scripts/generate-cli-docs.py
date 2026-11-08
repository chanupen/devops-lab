#!/usr/bin/env python3
"""Generate the CLI reference using help only, never command execution."""

import argparse
import difflib
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile


def generate(root):
    header = """# CLI reference

[Docs index](../README.md) · [TUI guide](tui.md) ·
[Profiles and stacks](profiles-and-stacks.md)

Generated from the command tree's `--help`. Do not edit this page by hand.
Every lab invocation below displays help only. It does not start a TUI, contact
AWS, inject a drill or change the lab.

> **Danger and cost:** without `--help`, up/down and exercise commands can
> change or destroy resources. Cloud apply/destroy requires approval and can
> incur charges. `assist eval --live` injects a fault; ordinary fixture eval
> does not. See the getting-started and task guides before running operations.
> `labs env` can print temporary credentials; never paste its output into Git.

## Regenerate or check

Run from the checkout with Python 3 and the module's Go toolchain.

```sh
python3 scripts/generate-cli-docs.py
```

**What you will see:** `wrote docs/guides/cli.md`. A temporary static binary is
built under ignored `bin/` and removed afterward. No installed binary changes.

```sh
python3 scripts/generate-cli-docs.py --check
```

**What you will see:** `CLI documentation is current`, or a diff and non-zero
exit status. CI runs this check. Update the generated page with command changes.

## Command help

"""
    (root / "bin").mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="cli-docs-", dir=root / "bin") as temp:
        binary = Path(temp) / "lab"
        subprocess.run(
            ["go", "build", "-buildvcs=false", "-ldflags", "-X main.version=docs",
             "-o", str(binary), "./cmd/lab"],
            cwd=root, env={**os.environ, "CGO_ENABLED": "0"}, check=True,
        )
        env = {**os.environ, "HOME": "/nonexistent", "LC_ALL": "C",
               "TERM": "dumb", "NO_COLOR": "1"}
        sections = []

        def collect(command):
            result = subprocess.run(
                [str(binary), *command, "--help"], cwd=temp, env=env,
                check=True, capture_output=True, text=True, timeout=20,
            )
            help_text = result.stdout.rstrip()
            invocation = " ".join(["lab", *command])
            sections.append(
                f"### {invocation}\n\n```sh\n{invocation} --help\n```\n\n"
                "**What you will see:** the help below. No operation is executed.\n\n"
                f"```text\n{help_text}\n```\n\n"
            )
            available = help_text.partition("Available Commands:\n")[2].split("\n\n", 1)[0]
            for name in sorted(re.findall(r"^  ([a-z][a-z0-9-]*)\s{2,}", available, re.M)):
                collect([*command, name])

        collect([])
    return header + "".join(sections).rstrip() + "\n"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true", help="fail if checked-in help differs")
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    destination = root / "docs/guides/cli.md"
    generated = generate(root)
    if args.check:
        existing = destination.read_text() if destination.exists() else ""
        if existing != generated:
            sys.stderr.writelines(difflib.unified_diff(
                existing.splitlines(keepends=True), generated.splitlines(keepends=True),
                fromfile="docs/guides/cli.md", tofile="generated CLI help",
            ))
            print("CLI documentation is stale; run python3 scripts/generate-cli-docs.py",
                  file=sys.stderr)
            return 1
        print("CLI documentation is current")
    else:
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.write_text(generated)
        print("wrote docs/guides/cli.md")
    return 0


if __name__ == "__main__":
    sys.exit(main())
