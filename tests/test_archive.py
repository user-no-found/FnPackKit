"""Security contract tests for package archive boundaries."""
import io
import tarfile
import unittest

from fnpackkit.archive import inventory


class ArchiveBoundary(unittest.TestCase):
    def parse(self, names, kind=tarfile.REGTYPE):
        buffer = io.BytesIO()
        with tarfile.open(fileobj=buffer, mode="w") as archive:
            for name in names:
                item = tarfile.TarInfo(name)
                item.type = kind
                item.linkname = "target" if kind == tarfile.SYMTYPE else ""
                item.size = 0
                archive.addfile(item, io.BytesIO())
        buffer.seek(0)
        with tarfile.open(fileobj=buffer) as archive:
            return inventory(archive)

    def test_reject_unsafe_entries(self):
        for names in (["../escape"], ["/absolute"], ["a\\b"], ["same", "same"], ["a", "a/b"]):
            with self.subTest(names=names), self.assertRaises(ValueError):
                self.parse(names)
        for kind in (tarfile.SYMTYPE, tarfile.LNKTYPE, tarfile.FIFOTYPE):
            with self.subTest(kind=kind), self.assertRaises(ValueError):
                self.parse(["entry"], kind)

    def test_regular_data(self):
        self.assertIn("etc/config.json", self.parse(["etc/config.json"]))
