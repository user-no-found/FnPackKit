"""Exercise checkout initialization and launch against isolated user configuration."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


@unittest.skipUnless(os.environ.get("FNPACKKIT_SETUP_E2E") == "1", "set FNPACKKIT_SETUP_E2E=1")
class SetupEntrypoints(unittest.TestCase):
    def test_register_and_launch_without_exports(self):
        project = Path(__file__).resolve().parents[1]
        buildflow = Path(os.environ["BUILDFLOW_SETUP_TEST_ROOT"]).resolve()
        with tempfile.TemporaryDirectory(prefix="fnpackkit setup '") as temporary:
            home = Path(temporary)
            environment = {key: value for key, value in os.environ.items()
                           if key not in {"HOME", "XDG_CONFIG_HOME", "BUILDFLOW_PATH",
                                          "FNPACKKIT_PATH", "PYTHONPATH"}}
            environment.update(HOME=str(home), XDG_CONFIG_HOME=str(home / "config"))

            def invoke(script, *args, extra=None):
                return subprocess.run([str(project / script), *map(str, args)],
                                      cwd=home, env={**environment, **(extra or {})},
                                      text=True, capture_output=True, timeout=20)

            missing = invoke("setup.sh")
            self.assertNotEqual(missing.returncode, 0)
            self.assertIn("BuildFlow is required", missing.stderr)
            setup = invoke("setup.sh", "--buildflow", buildflow)
            self.assertEqual(setup.returncode, 0, setup.stdout + setup.stderr)
            declaration = home / "config/buildflow/environment.json"
            data = json.loads(declaration.read_text())
            self.assertEqual(data["schema"], 1)
            self.assertEqual(data["tools"], {"BUILDFLOW_PATH": str(buildflow),
                                             "FNPACKKIT_PATH": str(project)})
            for script, args in (("setup.sh", ()), ("build.sh", ("--version",))):
                result = invoke(script, *args)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertEqual(json.loads(declaration.read_text()), data)
            for invalid in ("", str(home / "missing")):
                result = invoke("build.sh", "--version", extra={"BUILDFLOW_PATH": invalid})
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("Invalid BUILDFLOW_PATH", result.stderr)
            recovered = invoke("setup.sh", "--buildflow", buildflow,
                               extra={"BUILDFLOW_PATH": str(home / "missing")})
            self.assertEqual(recovered.returncode, 0, recovered.stdout + recovered.stderr)
