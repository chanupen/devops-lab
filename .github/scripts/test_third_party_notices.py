import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import third_party_notices as notices


class NoticesTest(unittest.TestCase):
    def test_toolchain_licences_from_tarball_or_packaged_location(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / "go"
            packaged = Path(tmp) / "licenses"
            root.mkdir()
            packaged.mkdir()
            with self.assertRaises(FileNotFoundError):
                notices.toolchain_files(root, packaged)
            for directory in [packaged, root]:
                (directory / "LICENSE").write_text("Go BSD licence")
                (directory / "PATENTS").write_text("Go patent grant")
                self.assertEqual(notices.toolchain_files(root, packaged),
                                 [directory / "LICENSE", directory / "PATENTS"])

    def test_retains_licence_and_separate_notice(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            (directory / "LICENSE").write_text("MPL licence")
            (directory / "NOTICE").write_text("Copyright notice")
            (directory / "README.md").write_text("Unrelated documentation")
            self.assertEqual(
                [p.name for p in notices.licence_files(directory)], ["LICENSE", "NOTICE"]
            )

    def test_readme_declaration_or_fail_closed(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            (directory / "NOTICE").write_text("No grant here")
            with self.assertRaises(ValueError):
                notices.licence_files(directory)
            (directory / "README.md").write_text("# Licence\nMIT\n")
            self.assertEqual(
                [p.name for p in notices.licence_files(directory)], ["NOTICE", "README.md"]
            )

    def test_union_of_platform_dependencies_and_replacement(self):
        first = {"Deps": [{"Path": "common", "Version": "v1"}, {"Path": "linux", "Version": "v1"}]}
        second = {"Deps": [
            {"Path": "common", "Version": "v1"},
            {"Path": "old", "Version": "v1", "Replace": {"Path": "replacement", "Version": "v2"}},
        ]}
        with patch.object(notices.subprocess, "check_output", side_effect=[json.dumps(first), json.dumps(second)]):
            self.assertEqual(
                set(notices.linked_modules(["linux", "darwin"])),
                {("common", "v1"), ("linux", "v1"), ("replacement", "v2")},
            )
        with patch.object(notices.subprocess, "check_output", return_value=json.dumps(
            {"Deps": [{"Path": "local", "Version": ""}]}
        )):
            with self.assertRaises(ValueError):
                notices.linked_modules(["binary"])


if __name__ == "__main__":
    unittest.main()
