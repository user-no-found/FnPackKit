"""Verify project branding bytes and provenance through actual FPK artifacts."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

from buildflow.contracts import BuildError
from fnpackkit.api import build
from fnpackkit.archive import inspect_fpk


@unittest.skipUnless(os.environ.get("FNPACKKIT_E2E") == "1", "set FNPACKKIT_E2E=1")
class BrandingFPK(unittest.TestCase):
    def test_branding_and_conflicts(self):
        from PIL import Image
        root = Path(tempfile.mkdtemp(prefix="fnpackkit-branding-"))
        print(f"Branding FPK evidence: {root}", flush=True)
        subprocess.run(["fnpack", "create", "brandfixture", "--without-ui"], cwd=root,
                       check=True, stdout=subprocess.PIPE)
        template = root / "brandfixture"
        for path in template.rglob(".DS_Store"):
            path.unlink()
        for name in ("ICON.PNG", "ICON_256.PNG"):
            (template / name).unlink()
        (template / "manifest").write_text(
            "appname=brandfixture\nversion=1.0.0\ndisplay_name=Brand fixture\n"
            "desc=Brand fixture\nsource=thirdparty\nplatform=all\n"
            "desktop_uidir=ui\ndesktop_applaunchname=brandfixture.main\n")
        (template / "app/ui").mkdir(parents=True)
        (template / "app/ui/config").write_text(json.dumps({".url": {"brandfixture.main": {
            "title": "Brand fixture", "icon": "images/icon_{0}.png", "type": "url",
            "protocol": "ignored-cgi-protocol", "port": "ignored-cgi-port",
            "url": "/cgi/ThirdParty/brandfixture/index.cgi"}}}))
        Image.new("RGBA", (320, 192), (90, 110, 130, 160)).save(root / "source.png")
        Image.new("RGB", (64, 64), (120, 80, 40)).save(root / "approved64.png")
        original = (root / "source.png").read_bytes()
        variant = (root / "approved64.png").read_bytes()
        project = root / "project.json"
        declaration = {"app_id": "brandfixture", "display_name": "Brand fixture",
                       "buildflow": {"schema": 1, "requires": ">=0.1,<0.2", "targets": ["fpk"], "steps": []},
                       "fnpackkit": {"schema": 1, "requires": ">=0.1,<0.2", "payload": "{project}/payload.json"},
                       "branding": {"icon": {"source": "source.png", "variants": {"64": "approved64.png"}}}}
        project.write_text(json.dumps(declaration))
        payload = {"schema": 1, "template": str(template), "files": [],
                   "values": {"APP_ID": "brandfixture", "VERSION": "1.0.0", "PLATFORM": "all"},
                   "artifact_name": "brandfixture.fpk"}
        (root / "payload.json").write_text(json.dumps(payload))
        result = build(project, ui=lambda event: None)
        artifact = result.output / "brandfixture.fpk"
        outer, inner = inspect_fpk(artifact)
        approved_hash = hashlib.sha256(variant).hexdigest()
        self.assertEqual(outer["ICON.PNG"]["sha256"], approved_hash)
        self.assertEqual(inner["ui/images/icon_64.png"]["sha256"], approved_hash)
        self.assertEqual(outer["ICON_256.PNG"]["sha256"], inner["ui/images/icon_256.png"]["sha256"])
        report = json.loads(Path(result.metadata["fpk_reports"][artifact.name]).read_text())
        self.assertIn("icons", report)
        self.assertEqual((root / "source.png").read_bytes(), original)
        self.assertEqual((root / "approved64.png").read_bytes(), variant)
        success = (root / ".build/last-success.json").read_bytes()
        (template / "ICON.PNG").write_bytes(variant)
        with self.assertRaisesRegex(BuildError, "branding icon mapping conflicts"):
            build(project, ui=lambda event: None)
        self.assertEqual((root / ".build/last-success.json").read_bytes(), success)
        (template / "ICON.PNG").unlink()
        Image.new("RGBA", (32, 32)).save(root / "approved64.png")
        with self.assertRaisesRegex(BuildError, "64"):
            build(project, ui=lambda event: None)
        self.assertEqual((root / ".build/last-success.json").read_bytes(), success)
        (root / "checks.json").write_text(json.dumps({
            "artifact": str(artifact), "explicit_variant_sha256": approved_hash,
            "four_mappings_verified": True, "source_preserved": True,
            "mapping_conflict_rejected": True, "invalid_variant_rejected": True}, indent=2) + "\n")
