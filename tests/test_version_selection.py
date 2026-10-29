"""Verify the FPK entry's version flow with real local manifests and text artifacts."""
import contextlib
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from buildflow.contracts import Artifact, BuildError
from fnpackkit.api import build
from fnpackkit.installed import installed_version


class FPKVersionSelectionTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="fnpackkit-version-selection-")
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.apps = self.root / "apps"
        self.apps.mkdir()
        self.project = self.root / "project.json"
        self.project.write_text(json.dumps({"app_id": "versionfixture", "display_name": "版本入口夹具",
            "buildflow": {"schema": 1, "targets": ["fpk"], "steps": []},
            "fnpackkit": {"schema": 1, "requires": ">=0.1,<0.2", "adapter": "adapter.py:plan"}}))
        (self.root / "adapter.py").write_text('''from buildflow.contracts import Step
from fnpackkit.contracts import Payload

def plan(project):
    version = project.data['version']
    (project.root / 'adapter-version.txt').write_text(version)
    def compile_version(ctx):
        assert ctx.project is project
        (ctx.work / 'compiled-version.txt').write_text(version)
    return {'steps': [Step('生成编译版本夹具', compile_version)],
            'metadata': {'adapter_version': version},
            'payload': lambda ctx: Payload(project.root, [],
                {'APP_ID': project.data['app_id'], 'VERSION': version, 'PLATFORM': 'x86'},
                'fixture_' + version + '.fpk')}
''')

    def installed(self, version):
        application = self.apps / "versionfixture"
        application.mkdir(exist_ok=True)
        (application / "manifest").write_text(f"appname=versionfixture\nversion={version}\n")

    def synthetic_package(self, context, payload, fnpack):
        version = context.project.data["version"]
        self.assertEqual(payload.values["VERSION"], version)
        self.assertEqual((context.work / "compiled-version.txt").read_text(), version)
        self.assertEqual((self.root / "adapter-version.txt").read_text(), version)
        artifact = context.work / "selected-version.txt"
        artifact.write_text(version)
        return [Artifact(artifact, f"selected_{version}.txt")]

    def run_build(self, **options):
        with patch("fnpackkit.api.installed_version", side_effect=lambda p: installed_version(p, self.apps)), \
                patch("fnpackkit.api.package", side_effect=self.synthetic_package), \
                contextlib.redirect_stderr(io.StringIO()):
            return build(self.project, ui=lambda _: None, **options)

    def check(self, expected):
        self.assertEqual(json.loads(self.project.read_text())["version"], expected)
        pointer = json.loads((self.root / ".build/last-success.json").read_text())
        report = json.loads((self.root / ".build" / pointer["report"]).read_text())
        self.assertEqual(report["version"], expected)
        self.assertEqual(report["metadata"]["adapter_version"], expected)
        self.assertEqual((self.root / "output" / f"selected_{expected}.txt").read_text(), expected)
        return report

    def test_missing_version_initializes_before_adapter_and_stable_resets(self):
        self.run_build(next_version=True, channel="test")
        self.check("0.0.0-01")
        self.run_build(next_version=True, channel="stable")
        self.assertEqual(self.check("0.0.1")["version_state"]["test_serial"], 0)

    def test_uninstalled_initial_stable_is_zero_zero_one(self):
        self.run_build(next_version=True, channel="stable")
        self.check("0.0.1")

    def test_installed_seed_then_successful_records_advance_without_installation(self):
        self.installed("0.0.5")
        for channel, expected in (("test", "0.0.5-01"), ("test", "0.0.5-02"),
                                  ("stable", "0.0.6"), ("test", "0.0.6-01")):
            self.run_build(next_version=True, channel=channel)
            self.check(expected)
        self.assertEqual((self.apps / "versionfixture/manifest").read_text(),
                         "appname=versionfixture\nversion=0.0.5\n")

    def test_installed_test_promotes_to_next_patch(self):
        self.installed("0.0.10-15")
        self.run_build(next_version=True, channel="stable")
        self.check("0.0.11")

    def test_packaging_failure_retries_without_consuming_candidate(self):
        with patch("fnpackkit.api.installed_version", return_value=None), \
                patch("fnpackkit.api.package", side_effect=BuildError("拒绝封包夹具")), \
                contextlib.redirect_stderr(io.StringIO()), self.assertRaisesRegex(BuildError, "拒绝封包夹具"):
            build(self.project, next_version=True, channel="test", ui=lambda _: None)
        self.assertFalse((self.root / ".build/last-success.json").exists())
        self.run_build(next_version=True)
        self.check("0.0.0-01")

    def test_invalid_installation_stops_before_adapter_or_version_write(self):
        self.installed("0.0.5")
        (self.apps / "versionfixture/manifest").write_text("appname=other\nversion=0.0.5\n")
        before = self.project.read_bytes()
        with self.assertRaisesRegex(BuildError, "appname does not match"):
            self.run_build(next_version=True)
        self.assertEqual(self.project.read_bytes(), before)
        self.assertFalse((self.root / "adapter-version.txt").exists())

    def test_noninteractive_requires_selection_and_manual_override_is_respected(self):
        before = self.project.read_bytes()
        with self.assertRaisesRegex(BuildError, "非交互构建"):
            self.run_build()
        self.assertEqual(self.project.read_bytes(), before)
        self.assertFalse((self.root / "adapter-version.txt").exists())
        self.run_build(build_version="7.8.9-15")
        self.check("7.8.9-15")


if __name__ == "__main__":
    unittest.main(verbosity=2)
