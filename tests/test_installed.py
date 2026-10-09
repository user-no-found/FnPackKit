"""Exercise local fnOS manifest identity, parsing and filesystem boundaries."""
import os
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest

from buildflow.contracts import BuildError
from fnpackkit.installed import installed_version


class InstalledManifest(unittest.TestCase):
    def test_manifest_boundary(self):
        project = SimpleNamespace(data={"app_id": "example"})
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.assertIsNone(installed_version(project, root))
            app = root / "example"
            app.mkdir()
            manifest = app / "manifest"
            with self.assertRaises(BuildError):
                installed_version(project, root)
            manifest.write_text("appname               = example\nversion               = 1.2610.817-819\n")
            self.assertEqual(installed_version(project, root), "1.2610.817-819")
            for value in ("appname=other\nversion=1.2.3\n", "appname=example\n",
                          "appname=example\nversion=1.2.3\nversion=1.2.4\n",
                          "appname=example\nversion=bad version\n", "x" * 65537):
                manifest.write_text(value)
                with self.subTest(value=value[:70]), self.assertRaises(BuildError):
                    installed_version(project, root)
            manifest.unlink()
            manifest.mkdir()
            with self.assertRaises(BuildError):
                installed_version(project, root)
            manifest.rmdir()
            os.mkfifo(manifest)
            with self.assertRaises(BuildError):
                installed_version(project, root)
            manifest.unlink()
            manifest.symlink_to(app / "missing")
            with self.assertRaises(BuildError):
                installed_version(project, root)
            manifest.unlink()
            manifest.write_text("appname=example\nversion=1.2.3\n")
            if os.geteuid() != 0:
                manifest.chmod(0)
                try:
                    with self.assertRaises(BuildError):
                        installed_version(project, root)
                finally:
                    manifest.chmod(0o600)
            with self.assertRaises(BuildError):
                installed_version(SimpleNamespace(data={"app_id": "../example"}), root)
