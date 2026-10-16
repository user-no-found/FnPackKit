"""Real fnpack archives must not hide outer members behind the inner payload."""
import hashlib
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import unittest

from fnpackkit.staging import digest, regular_tree
from fnpackkit.verification import verify_package


@unittest.skipUnless(os.environ.get("FNPACKKIT_E2E") == "1", "set FNPACKKIT_E2E=1 for native fnpack")
class ArchiveLayers(unittest.TestCase):
    def test_native_archive_and_cross_layer_collisions(self):
        parent = os.environ.get("FNPACKKIT_TEST_ARTIFACTS")
        if parent:
            Path(parent).mkdir(parents=True, exist_ok=True)
        root = Path(tempfile.mkdtemp(prefix="archive-layers-", dir=parent))
        fnpack = shutil.which("fnpack")
        self.assertIsNotNone(fnpack)
        subprocess.run([fnpack, "create", "layerfixture", "--without-ui"], cwd=root,
                       check=True, stdout=subprocess.PIPE, text=True)
        stage = root / "layerfixture"
        for path in stage.rglob(".DS_Store"):
            path.unlink()
        (stage / "app/bin").mkdir()
        (stage / "app/bin/program").write_bytes(b"expected payload\n")
        expected = {path.relative_to(stage).as_posix(): {
            "sha256": digest(path), "mode": path.stat().st_mode & 0o777, "size": path.stat().st_size,
        } for path in regular_tree(stage) if path.is_file()}
        subprocess.run([fnpack, "build", "--directory", str(stage)], cwd=root,
                       check=True, stdout=subprocess.PIPE, text=True)
        original = root / "layerfixture.fpk"
        verify_package(original, stage, expected)
        checks = [{"archive": original.name, "result": "accepted"}]
        for name, member_name, content in (
            ("different", "app/bin/program", b"unexpected payload\n"),
            ("same", "app/bin/program", b"expected payload\n"),
            ("normalized", "./app//bin/program", b"expected payload\n"),
        ):
            with self.subTest(name=name):
                altered = root / (name + ".fpk")
                with tarfile.open(original, "r:*") as source, tarfile.open(altered, "w:gz") as target:
                    for member in source:
                        if member.isfile():
                            with source.extractfile(member) as data:
                                target.addfile(member, data)
                        else:
                            target.addfile(member)
                    member = tarfile.TarInfo(member_name)
                    member.size = len(content)
                    member.mode = 0o644
                    target.addfile(member, io.BytesIO(content))
                with self.assertRaisesRegex(ValueError, "cross-layer.*app/bin/program"):
                    verify_package(altered, stage, expected)
                checks.append({"archive": altered.name, "result": "rejected",
                               "sha256": hashlib.sha256(altered.read_bytes()).hexdigest()})
        (root / "checks.json").write_text(json.dumps(checks, indent=2) + "\n")
        print(f"Archive layer evidence: {root}", flush=True)


if __name__ == "__main__":
    unittest.main()
