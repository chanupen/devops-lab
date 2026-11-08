"""Retain licences and source references for modules actually linked into releases."""

import argparse
import json
from pathlib import Path
import re
import subprocess
import sys


def licence_files(directory):
    files = sorted(
        p for p in directory.iterdir()
        if p.is_file() and p.name.lower().startswith(
            ("license", "licence", "copying", "notice", "patents")
        )
    )
    if not any(p.name.lower().startswith(("license", "licence", "copying")) for p in files):
        readmes = sorted(p for p in directory.iterdir() if p.name.lower().startswith("readme") and p.is_file())
        declarations = [
            p for p in readmes
            if re.search(r"\blicen[cs]e\b", p.read_text(), re.I)
            and re.search(r"\b(MIT|BSD|Apache|MPL|ISC)\b", p.read_text())
        ]
        if not declarations:
            raise ValueError(f"no licence or README licence declaration in {directory}")
        files.extend(declarations)
    return files


def source_path(value):
    return re.sub(r"[A-Z]", lambda match: "!" + match.group().lower(), value)


def linked_modules(binaries):
    modules = {}
    for binary in binaries:
        info = json.loads(subprocess.check_output(
            ["go", "version", "-m", "-json", str(binary)], text=True
        ))
        for dependency in info.get("Deps", []):
            module = dependency.get("Replace") or dependency
            if not module.get("Version"):
                raise ValueError(f"unversioned replacement: {module['Path']}")
            modules[module["Path"], module["Version"]] = module
    return modules


def toolchain_files(goroot, packaged=Path("/usr/share/licenses/golang")):
    for directory in [goroot, packaged]:
        if (directory / "LICENSE").is_file():
            return [p for p in [directory / "LICENSE", directory / "PATENTS"] if p.is_file()]
    raise FileNotFoundError("Go toolchain licence is missing")


def write_notices(binaries, output):
    output.write("Third-party notices for the modules linked into these lab binaries.\n")
    output.write("Model weights, container images and CI tools are not bundled.\n")
    output.write("README-only declarations retain the inventory's licence caveat; see docs/licences.md.\n")
    goroot = Path(subprocess.check_output(["go", "env", "GOROOT"], text=True).strip())
    for path in toolchain_files(goroot):
        output.write(f"\n===== Go toolchain: {path.name} =====\n{path.read_text()}\n")
    for (name, version), module in sorted(linked_modules(binaries).items()):
        download = json.loads(subprocess.check_output(
            ["go", "mod", "download", "-json", f"{name}@{version}"], text=True
        ))
        output.write(f"\n===== {name}@{version} =====\n")
        output.write(f"Module checksum: {module.get('Sum', download['Sum'])}\n")
        output.write(
            f"Corresponding source: https://proxy.golang.org/{source_path(name)}"
            f"/@v/{source_path(version)}.zip\n"
        )
        for path in licence_files(Path(download["Dir"])):
            output.write(f"\n--- {path.name} ---\n{path.read_text()}\n")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binaries", type=Path, nargs="+")
    write_notices(parser.parse_args().binaries, sys.stdout)
