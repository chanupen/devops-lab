from pathlib import Path
from io import StringIO
import tempfile
import unittest
from unittest.mock import patch

from check_docs import check, main


class DocsCheckTest(unittest.TestCase):
    def run_check(self, readme, files=None):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "README.md").write_text(readme)
            for name, text in (files or {}).items():
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                if isinstance(text, bytes):
                    path.write_bytes(text)
                else:
                    path.write_text(text)
            with patch("socket.socket", side_effect=AssertionError("network forbidden")), \
                    patch("socket.getaddrinfo", side_effect=AssertionError("DNS forbidden")):
                return check(root)[0]

    def test_links_and_github_anchors(self):
        text = """# **Same** `name`
# Same name
# Same name-1
<a id="custom"></a>
[first](#same-name) [second](#same-name-1) [third](#same-name-1-1)
[custom](#custom) ![asset](docs/a%20b.svg)
[nested [label]](docs/file%28copy%29.md#hello)
[named][ref] [ref][] [ref]
[ref]: <docs/file(copy).md#hello> "Title"
# [Header][ref]
[heading](#header)
"""
        self.assertEqual(self.run_check(text, {
            "docs/a b.svg": "<svg/>",
            "docs/file(copy).md": "Hello\n=====\n",
        }), [])

    def test_broken_targets_references_and_anchors(self):
        for text, expected in [
            ("[bad](missing.md)", "missing target"),
            ("[bad](#absent)", "missing anchor"),
            ("[bad][unknown]", "undefined link reference"),
            ("[bad](docs/file.md", "unclosed Markdown link"),
            ("[bad](../outside.md)", "escapes repository"),
        ]:
            with self.subTest(text=text):
                self.assertTrue(any(expected in e for e in self.run_check(text)))

    def test_fences_inline_code_html_and_line_links(self):
        text = """# Home
`[ignored](absent)` and \\[escaped](absent)
`multiline code
[ignored](absent)`
~~~text
[ignored](absent)
# Not an anchor
~~~
<a href="docs/file.md#section">HTML link</a>
[line](docs/file.md#L1-L2)
"""
        self.assertEqual(self.run_check(text, {"docs/file.md": "# Section\nbody\n"}), [])
        self.assertTrue(self.run_check("```mermaid\nflowchart LR\n"))
        self.assertTrue(self.run_check("[bad](docs/file.md#L9)", {"docs/file.md": "a\n"}))
        self.assertTrue(self.run_check("[bad](docs/image.png#L1)", {"docs/image.png": b"\x89PNG"}))

    def test_mermaid_nodes_arrows_and_links(self):
        diagram = """```mermaid
flowchart LR
A["Start"] --> B["End"]
A -. label .-> B
A-. version 1.2 .->B
B <--> A
click B "docs/end.md#end"
```
"""
        self.assertEqual(self.run_check(diagram, {"docs/end.md": "# End\n"}), [])
        for bad, expected in [
            (diagram.replace('B["End"]', 'B'), "undeclared node"),
            (diagram.replace("-->", "->"), "invalid arrow"),
            (diagram.replace('A["Start"]', 'A["Start"'), "invalid arrow"),
            (diagram.replace("#end", "#missing"), "missing anchor"),
            ("```mermaid\nsequenceDiagram\nA->>B: Hi\n```\n", "must start"),
            ("```mermaid\n```\n", "empty Mermaid"),
            ("```mermaid title=example\nflowchart LR\nA->B\n```\n", "invalid arrow"),
        ]:
            with self.subTest(expected=expected):
                errors = self.run_check(bad, {"docs/end.md": "# End\n"})
                self.assertTrue(any(expected in e for e in errors), errors)

    def test_external_urls_are_not_fetched(self):
        self.assertEqual(self.run_check("[external](https://invalid.example/a#unknown)"), [])

    def test_cli_fails_with_source_location(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "README.md").write_text("[broken](missing.md)\n")
            with patch("check_docs.__file__", str(root / "scripts/check_docs.py")), \
                    patch("sys.stderr", new_callable=StringIO) as errors:
                self.assertEqual(main(), 1)
                self.assertIn("README.md:1: missing.md: missing target", errors.getvalue())

    def test_escaped_names_angle_destinations_and_unicode(self):
        text = """[space](docs/a\\ b.md)
[apostrophe](docs/reader's.md "A title with )")
[angle](<docs/file(copy.md>)
[unicode](docs/a%20b.md#caf%C3%A9)
"""
        self.assertEqual(self.run_check(text, {
            "docs/a b.md": "# Caf\u00e9\n",
            "docs/reader's.md": "# Reader\n",
            "docs/file(copy.md": "# File\n",
        }), [])


if __name__ == "__main__":
    unittest.main()
