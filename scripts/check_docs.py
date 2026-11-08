#!/usr/bin/env python3
"""Check repository documentation without downloads, subprocesses or credentials."""

import html
from html.parser import HTMLParser
from pathlib import Path
import re
import sys
import unicodedata
from urllib.parse import unquote, urlsplit


def destination(text):
    text = text.strip()
    if text.startswith("<"):
        end = text.find(">")
        if end < 0:
            raise ValueError("unclosed link destination")
        return text[1:end]
    match = re.match(r"(?:\\.|[^\s])+", text)
    return re.sub(r"\\(.)", r"\1", match[0]) if match else ""


def closing(text, start, opener, closer):
    depth = 1
    quoted = None
    angled = False
    pos = start + 1
    while pos < len(text):
        char = text[pos]
        if char == "\\":
            pos += 2
            continue
        if opener == "(" and quoted is None and char == "<":
            angled = True
        elif angled and char == ">":
            angled = False
        if angled:
            pos += 1
            continue
        if opener == "(" and char in "\"'":
            if quoted == char:
                quoted = None
            elif quoted is None and text[pos - 1].isspace():
                quoted = char
        if quoted is None:
            if char == opener:
                depth += 1
            elif char == closer:
                depth -= 1
                if depth == 0:
                    return pos
        pos += 1
    return None


def mask_code(text):
    return re.sub(r"(`+).*?\1", lambda m: re.sub(r"[^\n]", " ", m[0]), text, flags=re.S)


def inline_links(text, references):
    text = mask_code(text)
    pos = 0
    while pos < len(text):
        if text[pos] == "\\":
            pos += 2
            continue
        if text[pos] != "[":
            pos += 1
            continue
        end = closing(text, pos, "[", "]")
        if end is None:
            pos += 1
            continue
        label = text[pos + 1:end]
        after = end + 1
        target = None
        if text[after:after + 1] == "(":
            finish = closing(text, after, "(", ")")
            if finish is None:
                raise ValueError("unclosed Markdown link")
            target = destination(text[after + 1:finish])
            after = finish + 1
        elif text[after:after + 1] == "[":
            finish = closing(text, after, "[", "]")
            if finish is None:
                raise ValueError("unclosed reference link")
            key = " ".join((text[after + 1:finish] or label).split()).casefold()
            if key not in references:
                raise ValueError(f"undefined link reference: {key}")
            target = references[key]
            after = finish + 1
        else:
            target = references.get(" ".join(label.split()).casefold())
        if target is not None:
            yield pos, after, label, target
        pos = after


def slug(text, references):
    for start, end, label, _ in reversed(list(inline_links(text, references))):
        text = text[:start] + label + text[end:]
    text = html.unescape(re.sub(r"<[^>]*>", "", text))
    text = re.sub(r"(`+)(.*?)\1", r"\2", text)
    text = re.sub(r"(\*\*|\*)(.*?)\1", r"\2", text)
    text = re.sub(r"(?<!\w)(__|_)(.*?)\1(?!\w)", r"\2", text)
    text = "".join(c for c in text.lower()
                   if c in "-_" or c.isspace()
                   or unicodedata.category(c)[0] in "LN")
    return text.replace(" ", "-")


class HTMLLinks(HTMLParser):
    def __init__(self):
        super().__init__()
        self.anchors = set()
        self.links = []

    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        if attrs.get("id"):
            self.anchors.add(attrs["id"])
        if tag == "a" and attrs.get("name"):
            self.anchors.add(attrs["name"])
        for attr in ("href", "src"):
            if attrs.get(attr):
                self.links.append((self.getpos()[0], attrs[attr]))


def mermaid(lines):
    """Validate the explicit-node flowchart subset documented in the style guide."""
    content = [(n, line.strip().removesuffix(";")) for n, line in lines
               if line.strip() and not line.lstrip().startswith("%% ")]
    if not content or not re.fullmatch(r"(flowchart|graph) (LR|RL|TD|TB|BT)", content[0][1]):
        raise ValueError("Mermaid must start with flowchart/graph and a direction")
    node = re.compile(r'([A-Za-z_]\w*(?:-\w+)*)(?:\["((?:\\.|[^"\\])*)"\])?')
    edge = re.compile(r"(<-->|-->|<--|---|==>|-\.\->|-\.\s+.+?\s+\.->)(?:\|[^|\n]+\|)?")
    declared, used, links = set(), [], []
    for number, line in content[1:]:
        click = re.fullmatch(r'click ([A-Za-z_][\w-]*) "([^"]+)"', line)
        if click:
            used.append((number, click[1]))
            links.append((number, click[2]))
            continue
        pos = 0
        while True:
            match = node.match(line, pos)
            if not match:
                raise ValueError(f"Mermaid line {number}: invalid node or unsupported syntax")
            used.append((number, match[1]))
            if match[2] is not None:
                declared.add(match[1])
                labels = HTMLLinks()
                labels.feed(re.sub(r'\\(["\\])', r"\1", match[2]))
                links.extend((number, target) for _, target in labels.links)
            pos = match.end()
            while pos < len(line) and line[pos].isspace():
                pos += 1
            if pos == len(line):
                break
            match = edge.match(line, pos)
            if not match:
                raise ValueError(f"Mermaid line {number}: invalid arrow or unsupported syntax")
            pos = match.end()
            while pos < len(line) and line[pos].isspace():
                pos += 1
    for number, name in used:
        if name not in declared:
            raise ValueError(f"Mermaid line {number}: undeclared node {name}")
    return links


def page(path):
    lines = path.read_text().splitlines()
    body, errors, diagram_links = [], [], []
    fence = None
    diagram = []
    diagrams = 0
    for number, line in enumerate(lines, 1):
        marker = re.match(r"^ {0,3}(`{3,}|~{3,})(.*)$", line)
        if fence:
            if marker and marker[1][0] == fence[0] and len(marker[1]) >= len(fence) and not marker[2].strip():
                if diagram:
                    try:
                        diagram_links.extend(mermaid(diagram))
                    except ValueError as error:
                        errors.append(f"{number}: {error}")
                elif is_mermaid:
                    errors.append(f"{number}: empty Mermaid block")
                fence = None
            elif is_mermaid:
                diagram.append((number, line))
            body.append("")
        elif marker:
            fence = marker[1]
            info = marker[2].strip().split()
            is_mermaid = bool(info) and info[0].casefold() == "mermaid"
            if is_mermaid:
                diagrams += 1
            diagram = []
            body.append("")
        else:
            body.append(line)
    if fence:
        errors.append(f"{len(lines)}: unclosed code fence")

    references = {}
    for number, line in enumerate(body, 1):
        match = re.match(r"^ {0,3}\[([^]]+)\]:\s*(.+)$", line)
        if match:
            key = " ".join(match[1].split()).casefold()
            try:
                references.setdefault(key, destination(match[2]))
            except ValueError as error:
                errors.append(f"{number}: {error}")
            body[number - 1] = ""
    anchors, links = set(), list(diagram_links)
    links.extend((1, target) for target in references.values())
    visible = mask_code("\n".join(body)).split("\n")
    for number, line in enumerate(body, 1):
        heading = re.match(r"^ {0,3}#{1,6}\s+(.+)$", line)
        setext = re.fullmatch(r" {0,3}(=+|-+)\s*", line)
        title = heading[1] if heading else body[number - 2] if setext and number > 1 else None
        try:
            if title:
                base = slug(re.sub(r"\s+#+\s*$", "", title), references)
                anchor, suffix = base, 0
                while anchor in anchors:
                    suffix += 1
                    anchor = f"{base}-{suffix}"
                anchors.add(anchor)
            links.extend((number, target) for _, _, _, target in inline_links(visible[number - 1], references))
        except ValueError as error:
            errors.append(f"{number}: {error}")
    tags = HTMLLinks()
    tags.feed("\n".join(visible))
    anchors.update(tags.anchors)
    links.extend(tags.links)
    return anchors, links, diagrams, errors


def check(root):
    root = root.resolve()
    paths = [root / "README.md"]
    for directory in ("docs", "drills", "labs"):
        paths.extend(sorted((root / directory).rglob("*.md")))
    pages, errors, count, diagrams = {}, [], 0, 0

    def load(path):
        if path not in pages:
            pages[path] = page(path)
        return pages[path]

    for path in paths:
        if not path.is_file():
            errors.append(f"{path.relative_to(root)}: missing documentation")
            continue
        _, links, diagram_count, problems = load(path)
        diagrams += diagram_count
        errors.extend(f"{path.relative_to(root)}:{problem}" for problem in problems)
        for number, target in links:
            try:
                url = urlsplit(html.unescape(target))
            except ValueError as error:
                errors.append(f"{path.relative_to(root)}:{number}: invalid URL: {error}")
                continue
            if url.scheme or url.netloc:
                continue
            count += 1
            name = unquote(url.path)
            dest = (root / name.lstrip("/") if name.startswith("/") else path.parent / name).resolve() if name else path
            prefix = f"{path.relative_to(root)}:{number}: {target}"
            if not dest.is_relative_to(root):
                errors.append(f"{prefix}: target escapes repository")
            elif not dest.exists():
                errors.append(f"{prefix}: missing target")
            elif url.fragment:
                fragment = unquote(url.fragment)
                if dest.is_dir():
                    dest = dest / "README.md"
                line = re.fullmatch(r"L(\d+)(?:-L(\d+))?", fragment)
                if line and dest.is_file():
                    try:
                        length = len(dest.read_text().splitlines())
                    except (OSError, UnicodeError):
                        errors.append(f"{prefix}: line anchor requires readable text")
                        continue
                    if not 1 <= int(line[1]) <= int(line[2] or line[1]) <= length:
                        errors.append(f"{prefix}: invalid line range")
                elif dest.suffix.lower() in (".md", ".html") and dest.is_file():
                    if fragment not in load(dest)[0]:
                        errors.append(f"{prefix}: missing anchor")
                else:
                    errors.append(f"{prefix}: anchor requires Markdown/HTML or a line range")
    return errors, len(paths), count, diagrams


def main():
    errors, pages, links, diagrams = check(Path(__file__).resolve().parent.parent)
    if errors:
        print("\n".join(errors), file=sys.stderr)
        return 1
    print(f"Docs OK: {pages} pages, {links} local links, {diagrams} Mermaid blocks (offline; external URLs skipped)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
