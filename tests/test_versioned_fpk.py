"""Exercise channel selection and flat publication through the real FPK CLI."""
import json
import os
from pathlib import Path
import pty
import select
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
import unittest

from buildflow.contracts import BuildError
from fnpackkit.api import build
from fnpackkit.contracts import Payload
from fnpackkit.formats import manifest_fields


@unittest.skipUnless(os.environ.get("FNPACKKIT_E2E") == "1", "set FNPACKKIT_E2E=1 for real fnpack")
class VersionedFPK(unittest.TestCase):
    def test_cli_versions_and_locked_adapter(self):
        executable = shutil.which("fnpack")
        self.assertIsNotNone(executable)
        root = Path(tempfile.mkdtemp(prefix="fnpackkit-version-e2e-"))
        print(f"Versioned FPK evidence: {root}", flush=True)
        subprocess.run([executable, "create", "example", "--without-ui"], cwd=root,
                       check=True, stdout=subprocess.PIPE, text=True)
        template = root / "example"
        for path in template.rglob(".DS_Store"):
            path.unlink()
        (template / "manifest").unlink()
        (template / "manifest.in").write_text(
            "appname=@APP_ID@\nversion=@VERSION@\ndisplay_name=Version fixture\n"
            "desc=Version selection\nplatform=@PLATFORM@\nsource=thirdparty\n"
            "maintainer=Example\ndistributor=Example\n")
        (template / "app/data.txt").write_text("native version fixture\n")
        project = root / "project.json"
        project.write_text(json.dumps({"app_id": "example", "display_name": "Version fixture",
            "version": "1.2.3", "buildflow": {"schema": 1, "targets": ["fpk"], "steps": []},
            "fnpackkit": {"schema": 1, "requires": ">=0.1,<0.2", "adapter": "adapter.py:plan"}}))
        (root / "adapter.py").write_text('''
from fnpackkit.contracts import Payload

def plan(project):
    version = project.data['version']
    return {'steps': [], 'metadata': {'adapter_version': version},
            'payload': lambda ctx: Payload(project.root / 'example', [],
                {'APP_ID': project.data['app_id'], 'VERSION': version, 'PLATFORM': 'x86'},
                'example_' + version + '_x86.fpk')}
''')
        command = [sys.executable, "-m", "fnpackkit", "--project", str(project), "--fnpack", executable]
        denied = subprocess.run(command, cwd=root, capture_output=True, text=True)
        self.assertNotEqual(denied.returncode, 0)
        self.assertIn("非交互构建必须指定", denied.stderr)
        self.assertEqual(json.loads(project.read_text())["version"], "1.2.3")

        def check(version):
            self.assertEqual(json.loads(project.read_text())["version"], version)
            pointer = json.loads((root / ".build/last-success.json").read_text())
            report = json.loads((root / ".build" / pointer["report"]).read_text())
            self.assertEqual(report["metadata"]["adapter_version"], version)
            archive = root / "output" / ("example_" + version + "_x86.fpk")
            with tarfile.open(archive) as package:
                self.assertEqual(manifest_fields(package.extractfile("manifest").read().decode())["version"], version)
            self.assertTrue(all(path.is_file() and path.name.endswith((".fpk", ".fpk.sha256"))
                                for path in (root / "output").iterdir()))

        for flags, expected in [(["--next-version", "--channel", "stable"], "1.2.4"),
                                (["--next-version", "--channel", "test"], "1.2.4-01"),
                                (["--build-version", "2.4.6-02"], "2.4.6-02"),
                                (["--next-version", "--channel", "stable"], "2.4.6")]:
            result = subprocess.run(command + flags, cwd=root, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn("已安装版本: 未安装", result.stderr)
            check(expected)

        master, slave = pty.openpty()
        process = subprocess.Popen(command, cwd=root, stdin=slave, stdout=slave, stderr=slave)
        os.close(slave)
        output = bytearray()
        try:
            os.write(master, b"test\n\n")
            deadline = time.monotonic() + 15
            while time.monotonic() < deadline:
                if select.select([master], [], [], 0.1)[0]:
                    try:
                        chunk = os.read(master, 65536)
                    except OSError:
                        break
                    if not chunk:
                        break
                    output.extend(chunk)
                if process.poll() is not None:
                    break
            self.assertEqual(process.wait(timeout=2), 0, output.decode(errors="replace"))
        finally:
            os.close(master)
            if process.poll() is None:
                process.kill()
                process.wait()
        self.assertIn("项目版本: 2.4.6", output.decode())
        self.assertIn("构建类型", output.decode())
        check("2.4.6-01")
        previous = (root / ".build/last-success.json").read_bytes()
        mismatch = Payload(template, [], {"APP_ID": "example", "VERSION": "0.0.1", "PLATFORM": "x86"}, "wrong.fpk")
        with self.assertRaisesRegex(BuildError, "payload VERSION must equal"):
            build(project, steps=[], payload=mismatch, build_version="3.0.0", fnpack=executable,
                  ui=lambda event: None)
        self.assertEqual((root / ".build/last-success.json").read_bytes(), previous)
        self.assertEqual(json.loads(project.read_text())["version"], "3.0.0")
