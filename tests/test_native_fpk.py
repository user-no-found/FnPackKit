"""Build a generic text payload with the real native fnpack and retain its artifacts."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import tarfile
import unittest

from buildflow.contracts import BuildError
from fnpackkit.api import build
from fnpackkit.archive import inspect_fpk
from fnpackkit.contracts import Payload, PayloadFile
from fnpackkit.discovery import discover


@unittest.skipUnless(os.environ.get("FNPACKKIT_E2E") == "1", "set FNPACKKIT_E2E=1 for real fnpack")
class NativeFPK(unittest.TestCase):
    def test_native_payload_and_failure_preserves_success(self):
        executable = shutil.which("fnpack")
        self.assertIsNotNone(executable)
        root = Path(tempfile.mkdtemp(prefix="fnpackkit-e2e-"))
        print(f"Native FPK fixture and artifacts: {root}", flush=True)
        subprocess.run([executable, "create", "fixture", "--without-ui"], cwd=root,
                       check=True, stdout=subprocess.PIPE, text=True)
        template = root / "fixture"
        for path in template.rglob(".DS_Store"):
            path.unlink()
        (template / "manifest").unlink()
        (template / "manifest.in").write_text(
            "appname=@APP_ID@\nversion=@VERSION@\ndisplay_name=Native fixture\n"
            "desc=Generic FPK\nplatform=@PLATFORM@\nsource=thirdparty\n"
            "maintainer=Example\ndistributor=Example\n")
        (root / "hello.txt").write_text("Generic data payload; no language-specific binary contract.\n")
        project = root / "project.json"
        project.write_text(json.dumps({"app_id": "fixture", "display_name": "Native fixture",
            "buildflow": {"schema": 1, "requires": ">=0.1,<0.2", "targets": ["fpk"], "steps": []},
            "fnpackkit": {"schema": 1, "requires": ">=0.1,<0.2", "payload": "{project}/payload.json"}}))
        manifest = {"schema": 1, "template": str(template), "files": [
            {"source": str(root / "hello.txt"), "destination": "app/data/hello.txt", "mode": 0o644}],
            "values": {"APP_ID": "fixture", "VERSION": "1.0.0", "PLATFORM": "x86"},
            "artifact_name": "fixture_1.0.0_x86.fpk", "required": ["app/data/hello.txt"]}
        (root / "payload.json").write_text(json.dumps(manifest))
        candidates = discover([root], max_depth=1)
        self.assertEqual([item.app_id for item in candidates.candidates], ["fixture"])
        result = build(project, build_version="1.0.0", fnpack=executable, ui=lambda event: None)
        artifact = result.output / manifest["artifact_name"]
        with tarfile.open(artifact) as package:
            from fnpackkit.formats import manifest_fields
            fields = manifest_fields(package.extractfile("manifest").read().decode())
        self.assertEqual(fields["maintainer"], "user-no-found")
        self.assertEqual(fields["distributor"], "user-no-found")
        self.assertEqual(fields["maintainer_url"], "https://github.com/user-no-found")
        self.assertEqual(fields["distributor_url"], "https://github.com/user-no-found")
        _, application = inspect_fpk(artifact)
        self.assertIn("data/hello.txt", application)
        self.assertTrue(artifact.with_suffix(".fpk.sha256").is_file())
        self.assertFalse(artifact.with_suffix(".build.json").exists())
        self.assertTrue(Path(result.metadata["fpk_reports"][artifact.name]).is_file())
        previous = (root / ".build/last-success.json").read_bytes()
        # A second architecture uses the same invocation and shared runner.
        payload = Payload(template, [PayloadFile(root / "hello.txt", "app/data/hello.txt")],
                          manifest["values"], "fixture_1.0.0_second.fpk")
        bad = Payload(template, [PayloadFile(root / "hello.txt", "../escape")],
                      manifest["values"], "bad.fpk")
        with self.assertRaises(BuildError):
            build(project, build_version="1.0.0", steps=[], payload=[payload, bad], fnpack=executable, ui=lambda event: None)
        self.assertEqual(previous, (root / ".build/last-success.json").read_bytes())
        self.assertTrue(artifact.is_file())
        self.assertFalse((root / "escape").exists())

        # Verification receives this run's immutable artifact declarations after packaging.
        def reject_actual_package(ctx, packaged):
            self.assertIsInstance(packaged, tuple)
            packages = [Path(item.path) for item in packaged if item.name.endswith(".fpk")]
            self.assertEqual(len(packages), 1)
            self.assertNotEqual(packages[0], artifact)
            self.assertIn("data/hello.txt", inspect_fpk(packages[0])[1])
            raise ValueError("application verification rejected actual FPK")

        def forbidden_reports(ctx):
            self.fail("extra reports evaluated after failed verification")

        with self.assertRaisesRegex(BuildError, "application verification rejected"):
            build(project, build_version="1.0.0", fnpack=executable, verify=reject_actual_package,
                  artifacts=forbidden_reports, ui=lambda event: None)
        self.assertEqual(previous, (root / ".build/last-success.json").read_bytes())
        with self.assertRaisesRegex(BuildError, "verify must return None"):
            build(project, build_version="1.0.0", fnpack=executable, verify=lambda ctx, packaged: False,
                  ui=lambda event: None)
        self.assertEqual(previous, (root / ".build/last-success.json").read_bytes())

        # The selected adapter exposes verification and reports through the same public plan.
        (root / "adapter.py").write_text("""
import json
from pathlib import Path
from buildflow.contracts import Artifact
from fnpackkit.archive import inspect_fpk
from fnpackkit.manifest import read_payload

def plan(project):
    def verify(ctx, packaged):
        assert isinstance(packaged, tuple)
        packages = [Path(item.path) for item in packaged if item.name.endswith('.fpk')]
        assert len(packages) == 1
        assert 'data/hello.txt' in inspect_fpk(packages[0])[1]
        (ctx.work / 'application-check.json').write_text(json.dumps({'verified': True, 'package': packages[0].name}))
    def artifacts(ctx):
        return [Artifact(ctx.work / 'application-check.json', 'checks/application-check.json')]
    return {'steps': [], 'payload': lambda ctx: read_payload(project.root / 'payload.json'),
            'verify': verify, 'artifacts': artifacts}
""")
        declaration = json.loads(project.read_text())
        declaration["fnpackkit"].pop("payload")
        declaration["fnpackkit"]["adapter"] = "adapter.py:plan"
        project.write_text(json.dumps(declaration))
        manifest["artifact_name"] = "fixture_1.0.0_verified.fpk"
        (root / "payload.json").write_text(json.dumps(manifest))
        verified = build(project, build_version="1.0.0", fnpack=executable, ui=lambda event: None)
        self.assertTrue((verified.output / manifest["artifact_name"]).is_file())
        records = verified.metadata["application_reports"]
        self.assertEqual(records[0]["name"], "checks/application-check.json")
        report = json.loads(Path(records[0]["path"]).read_text())
        self.assertFalse((verified.output / "checks").exists())
        self.assertTrue(all(path.name.endswith((".fpk", ".fpk.sha256")) for path in verified.output.iterdir()))
        self.assertEqual(report, {"verified": True, "package": manifest["artifact_name"]})
        self.assertNotEqual(previous, (root / ".build/last-success.json").read_bytes())
        self.assertTrue(artifact.is_file())
