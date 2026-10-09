"""Exercise publisher policy through native generic FPK packaging and its archive checks."""
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import unittest

from buildflow.contracts import BuildError
from fnpackkit.api import build
from fnpackkit.formats import manifest_fields


@unittest.skipUnless(os.environ.get("FNPACKKIT_E2E") == "1", "set FNPACKKIT_E2E=1 for real fnpack")
class PublisherFPK(unittest.TestCase):
    def test_shared_identity_and_invalid_manifests(self):
        executable = shutil.which("fnpack")
        self.assertIsNotNone(executable)
        root = Path(tempfile.mkdtemp(prefix="fnpackkit-publisher-e2e-"))
        print(f"Publisher FPK fixture and artifacts: {root}", flush=True)
        expected = {
            "maintainer": "user-no-found", "maintainer_url": "https://github.com/user-no-found",
            "distributor": "user-no-found", "distributor_url": "https://github.com/user-no-found",
        }
        license_text = b"Upstream fixture license and copyright remain with their authors.\n"
        checks = []
        for app_id, profile in (
            ("publisherabsent", ""),
            ("publisheroverride", "maintainer=Other author\nmaintainer_url=https://author.example\n"
             "distributor=Other publisher\ndistributor_url=https://publisher.example\n"),
        ):
            with self.subTest(app_id=app_id):
                project_root = root / app_id
                project_root.mkdir()
                subprocess.run([executable, "create", app_id, "--without-ui"], cwd=project_root,
                               check=True, stdout=subprocess.PIPE, text=True)
                template = project_root / app_id
                for path in template.rglob(".DS_Store"):
                    path.unlink()
                manifest_path = template / "manifest"
                source_manifest = (
                    f"appname={app_id}\nversion=1.0.0\ndisplay_name=Publisher fixture\n"
                    "desc=Generic publisher policy fixture\nplatform=x86\nsource=thirdparty\n" + profile)
                manifest_path.write_text(source_manifest, encoding="utf-8")
                (template / "app" / "LICENSE").write_bytes(license_text)
                project = project_root / "project.json"
                project.write_text(json.dumps({
                    "app_id": app_id, "display_name": "Publisher fixture",
                    "buildflow": {"schema": 1, "requires": ">=0.1,<0.2", "targets": ["fpk"], "steps": []},
                    "fnpackkit": {"schema": 1, "requires": ">=0.1,<0.2", "payload": "{project}/payload.json"},
                }))
                (project_root / "payload.json").write_text(json.dumps({
                    "schema": 1, "template": str(template), "files": [],
                    "values": {"APP_ID": app_id, "VERSION": "1.0.0", "PLATFORM": "x86"},
                    "artifact_name": f"{app_id}.fpk", "required": ["app/LICENSE"],
                }))
                result = build(project, fnpack=executable, ui=lambda event: None)
                artifact = result.output / f"{app_id}.fpk"
                with tarfile.open(artifact) as package:
                    fields = manifest_fields(package.extractfile("manifest").read().decode("utf-8"))
                    application = package.extractfile("app.tgz").read()
                self.assertEqual({key: fields[key] for key in expected}, expected)
                self.assertEqual(fields["source"], "thirdparty")
                self.assertEqual(fields["appname"], app_id)
                with tarfile.open(fileobj=io.BytesIO(application)) as app:
                    license_member = next(member for member in app if member.name.removeprefix("./") == "LICENSE")
                    self.assertEqual(app.extractfile(license_member).read(), license_text)
                self.assertEqual(manifest_path.read_text(encoding="utf-8"), source_manifest)
                self.assertTrue(artifact.with_suffix(".fpk.sha256").is_file())
                checks.append({"app_id": app_id, "archive": str(artifact), "profile": expected,
                               "source_and_license_preserved": True})

        success = (project_root / ".build" / "last-success.json").read_bytes()
        for name, invalid in (
            ("duplicate", "maintainer=duplicate\n"),
            ("malformed", "maintainer without equals\n"),
        ):
            with self.subTest(invalid=name):
                text = source_manifest + invalid
                manifest_path.write_text(text, encoding="utf-8")
                with self.assertRaisesRegex(BuildError, "invalid or duplicate manifest field"):
                    build(project, fnpack=executable, ui=lambda event: None)
                self.assertEqual(manifest_path.read_text(encoding="utf-8"), text)
                self.assertEqual((project_root / ".build" / "last-success.json").read_bytes(), success)
                staged = list((project_root / ".build").glob("*/fpk/*/stage/manifest"))
                self.assertTrue(any(path.read_text(encoding="utf-8") == text for path in staged))
                checks.append({"rejected": name, "invalid_input_preserved": True})
        (root / "publisher-checks.json").write_text(json.dumps({"checks": checks}, indent=2) + "\n")
