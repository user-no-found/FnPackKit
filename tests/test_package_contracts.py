"""Exercise package declaration boundaries through real native FPK builds."""
import copy
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

from buildflow.contracts import BuildError
from fnpackkit.api import build
from fnpackkit.archive import inspect_fpk


@unittest.skipUnless(os.environ.get("FNPACKKIT_E2E") == "1", "set FNPACKKIT_E2E=1")
class PackageContracts(unittest.TestCase):
    def test_declared_contracts(self):
        root = Path(tempfile.mkdtemp(prefix="fnpackkit-contracts-"))
        print(f"Package contract evidence: {root}", flush=True)
        subprocess.run(["fnpack", "create", "contractfixture", "--without-ui"], cwd=root,
                       check=True, stdout=subprocess.PIPE)
        template = root / "contractfixture"
        for path in template.rglob(".DS_Store"):
            path.unlink()
        manifest = ("appname=contractfixture\nversion=1.0.0\ndisplay_name=Contracts\n"
                    "desc=Contracts fixture\nplatform=x86\nsource=thirdparty\n"
                    "desktop_uidir=ui\ndesktop_applaunchname=contractfixture.main\n")
        ui = template / "app/ui"
        (ui / "images").mkdir(parents=True)
        for size, name in ((64, "ICON.PNG"), (256, "ICON_256.PNG")):
            shutil.copyfile(template / name, ui / f"images/icon_{size}.png")
        entry = {"title": "Fixture", "icon": "images/icon_{0}.png", "type": "iframe",
                 "protocol": "ignored-gateway-value", "port": "ignored-gateway-value",
                 "url": "/app/contractfixture/", "gatewayPrefix": "/app/contractfixture",
                 "gatewaySocket": "fixture.sock", "allUsers": True,
                 "future-extension": {"accepted": True}}
        project_data = {"app_id": "contractfixture", "display_name": "Contracts",
                        "buildflow": {"schema": 1, "requires": ">=0.1,<0.2", "targets": ["fpk"], "steps": []},
                        "fnpackkit": {"schema": 1, "requires": ">=0.1,<0.2", "payload": "{project}/payload.json"}}
        project = root / "project.json"
        payload = {"schema": 1, "template": str(template), "files": [],
                   "values": {"APP_ID": "contractfixture", "VERSION": "1.0.0", "PLATFORM": "x86"},
                   "artifact_name": "contractfixture.fpk"}
        resource = {"future-extension": {}, "data-share": {"shares": [{"name": "fixture/data"}]}}
        privilege = {"defaults": {"run-as": "root"}, "username": "fixture", "future-extension": True}
        checks = []

        def reset():
            (template / "manifest").write_text(manifest)
            (ui / "config").write_text(json.dumps({".url": {"contractfixture.main": entry}}))
            (template / "config/resource").write_text(json.dumps(resource))
            (template / "config/privilege").write_text(json.dumps(privilege))
            (template / "wizard/install").write_text(json.dumps([
                {"stepTitle": "Fixture", "items": [{"type": "text", "field": "wizard_port", "label": "Port"}]}]))
            project.write_text(json.dumps(project_data))
            (root / "payload.json").write_text(json.dumps(payload))

        reset()
        good = build(project, build_version="1.0.0", ui=lambda event: None)
        self.assertIn("ui/config", inspect_fpk(good.output / payload["artifact_name"])[1])
        success = (root / ".build/last-success.json").read_bytes()
        checks.append({"valid_gateway_root_and_extensions": True})

        def invalid(name, mutate, expected):
            reset()
            mutate()
            with self.subTest(name=name), self.assertRaisesRegex(BuildError, expected):
                build(project, build_version="1.0.0", ui=lambda event: None)
            self.assertEqual(success, (root / ".build/last-success.json").read_bytes())
            checks.append({"rejected": name})

        def change_json(path, field, value):
            data = json.loads(path.read_text())
            data[field] = value
            path.write_text(json.dumps(data))

        def change_entry(field, value):
            changed = {**entry, field: value}
            (ui / "config").write_text(json.dumps({".url": {"contractfixture.main": changed}}))

        invalid("launcher", lambda: (template / "manifest").write_text(
            manifest.replace("contractfixture.main", "contractfixture.missing")), "desktop_applaunchname")
        invalid("uidir traversal", lambda: (template / "manifest").write_text(
            manifest.replace("desktop_uidir=ui", "desktop_uidir=../escape")), "relative path")
        invalid("icon missing", lambda: change_entry("icon", "missing_{0}.png"), "icon")
        invalid("gateway boundary", lambda: change_entry("gatewayPrefix", "/app/contractfixture2"), "gatewayPrefix")
        invalid("gateway traversal", lambda: change_entry("gatewayPrefix", "/app/contractfixture/../x"), "gatewayPrefix")
        invalid("socket path", lambda: change_entry("gatewaySocket", "../x.sock"), "gatewaySocket")
        invalid("URL boundary", lambda: change_entry("url", "/app/contractfixture2/"), "gateway.*url")
        invalid("visibility type", lambda: change_entry("allUsers", "true"), "allUsers")
        invalid("privilege mode", lambda: change_json(template / "config/privilege", "defaults", {"run-as": "admin"}), "run-as")
        invalid("resource link", lambda: change_json(template / "config/resource", "usr-local-linker", {"bin": ["../outside"]}), "relative path")
        invalid("API shape", lambda: change_json(template / "config/resource", "api-scope", {}), "api-scope")
        invalid("wizard reserved", lambda: (template / "wizard/install").write_text(json.dumps([
            {"stepTitle": "Fixture", "items": [{"type": "text", "field": "TRIM_APPDEST"}]}])), "TRIM_")
        invalid("duplicate JSON", lambda: (template / "config/resource").write_text('{"api-scope":[],"api-scope":[]}'), "duplicate")
        def invalid_platform():
            (template / "manifest").write_text(manifest.replace("platform=x86", "platform=amd64"))
            changed = copy.deepcopy(payload)
            changed["values"]["PLATFORM"] = "amd64"
            (root / "payload.json").write_text(json.dumps(changed))
        invalid("platform spelling", invalid_platform, "platform must")
        def capabilities(value):
            data = copy.deepcopy(project_data)
            data["fnpackkit"]["capabilities"] = value
            project.write_text(json.dumps(data))
        invalid("SDK micro_app", lambda: capabilities({"js_sdk": True}), "micro_app")
        invalid("backend scope", lambda: capabilities({"api_scopes": ["trim.system.getPlatformConfig"]}), "api-scope")
        reset()
        capabilities({"js-sdk": True})
        with self.assertRaisesRegex(ValueError, "unknown fnpackkit.capabilities"):
            build(project, build_version="1.0.0", ui=lambda event: None)
        checks.append({"rejected": "capability typo"})
        reset()
        capabilities({"js_sdk": True})
        (template / "manifest").write_text(manifest + "micro_app=true\n")
        payload["artifact_name"] = "contractfixture_frontend.fpk"
        (root / "payload.json").write_text(json.dumps(payload))
        build(project, build_version="1.0.0", ui=lambda event: None)
        checks.append({"frontend_sdk_without_backend_scope": True})
        reset()
        capabilities({"js_sdk": True, "api_scopes": ["trim.system.getPlatformConfig"]})
        (template / "manifest").write_text(manifest + "micro_app=true\n")
        change_json(template / "config/resource", "api-scope", ["trim.system.getPlatformConfig"])
        payload["artifact_name"] = "contractfixture_capable.fpk"
        (root / "payload.json").write_text(json.dumps(payload))
        build(project, build_version="1.0.0", ui=lambda event: None)
        checks.append({"declared_capabilities": True})
        (root / "checks.json").write_text(json.dumps(checks, indent=2) + "\n")
