"""Status is read-only and root payload repair preserves active runtime IPC."""
import dataclasses
import os
from pathlib import Path
import socket
import stat
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from fnpackkit.contracts import Lifecycle, LifecycleCommand, UninstallPolicy
from fnpackkit.lifecycle import generate_lifecycle
from fnpackkit.uninstall import generate_uninstall
from fnpackkit import uninstall_runtime as runtime


class StatusScriptTests(unittest.TestCase):
    def test_repeated_status_preserves_socket_and_never_calls_prepare(self):
        with tempfile.TemporaryDirectory(prefix="fpk-status-", dir="/tmp") as temporary:
            root = Path(temporary)
            stage = root / "package"
            (stage / "app/bin").mkdir(parents=True)
            (stage / "config").mkdir()
            (stage / "config/privilege").write_text('{"defaults":{"run-as":"root"}}')
            hook = stage / "app/bin/service"
            hook.write_text('#!/bin/sh\n[ "$1" = status ] && exit 3\nexit 0\n')
            hook.chmod(0o755)
            lifecycle = Lifecycle({"main:"+name: LifecycleCommand("target", "bin/service", (name,))
                                   for name in ("start", "stop", "status")})
            generate_lifecycle(stage, lifecycle)
            policy = UninstallPolicy(("etc/settings",), ("var/transient",),
                                     "数据", "数据", "保留", "删除", state_owner="root")
            generate_uninstall(stage, policy, "example", lifecycle)
            # A sentinel makes any preparation during a read visibly fail.
            helper = stage / "cmd/hooks/fnpack_uninstall.py"
            marker = root / "prepare-called"
            helper.write_text('from pathlib import Path\nPath('+repr(str(marker))+').write_text("called")\nraise SystemExit(27)\n')
            path = stage / "app/backend.sock"
            with socket.socket(socket.AF_UNIX) as server:
                server.bind(str(path))
                server.listen(1)
                path.chmod(0o660)
                before = path.stat()
                env = {"PATH": "/usr/bin:/bin", "TRIM_APPDEST": str(stage / "app")}
                for _ in range(3):
                    result = subprocess.run(["/bin/sh", str(stage / "cmd/main"), "status"],
                                            env=env, capture_output=True, text=True)
                    self.assertEqual(result.returncode, 3, result.stderr)
                result = subprocess.run(["/bin/sh", str(stage / "cmd/main"), "stop"],
                                        env=env, capture_output=True, text=True)
                self.assertEqual(result.returncode, 0, result.stderr)
                after = path.stat()
                self.assertEqual((after.st_uid, after.st_gid, after.st_mode, after.st_ctime_ns),
                                 (before.st_uid, before.st_gid, before.st_mode, before.st_ctime_ns))
                self.assertFalse(marker.exists())
                result = subprocess.run(["/bin/sh", str(stage / "cmd/main"), "start"],
                                        env=env, capture_output=True, text=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertTrue(marker.exists(), "start must retain ownership preparation")


class RuntimeOwnershipTests(unittest.TestCase):
    def fixture(self, root):
        (root / "@appcenter/example").mkdir(parents=True)
        cleanup = object.__new__(runtime.Cleanup)
        cleanup.volume_fd = os.open(root, runtime.FLAGS)
        self.addCleanup(os.close, cleanup.volume_fd)
        cleanup.app = "example"
        cleanup.roots = {}
        cleanup.mounts = set()
        cleanup.uid = os.getuid()
        cleanup.device = os.fstat(cleanup.volume_fd).st_dev
        return cleanup, root / "@appcenter/example"

    def test_root_payload_repair_does_not_chown_live_socket_or_fifo(self):
        with tempfile.TemporaryDirectory(prefix="fpk-ipc-", dir="/tmp") as temporary:
            cleanup, app = self.fixture(Path(temporary))
            payload = app / "program"
            payload.write_text("synthetic executable")
            payload.chmod(0o775)
            fifo = app / "events.pipe"
            os.mkfifo(fifo, 0o660)
            path = app / "backend.sock"
            with socket.socket(socket.AF_UNIX) as server:
                server.bind(str(path)); server.listen(1); path.chmod(0o660)
                before = path.stat()
                owned = []
                def record_chown(name, uid, gid):
                    info = os.stat(name)
                    owned.append((info.st_ino, uid, gid))
                # Only root authority/chown is simulated; scan, fd traversal,
                # inode validation, mode repair and live socket are real.
                with patch.object(runtime, "trusted"), patch.object(runtime.os, "chown", side_effect=record_chown), \
                     patch.dict(os.environ, {"TRIM_APPDEST": str(app)}):
                    for _ in range(3):
                        cleanup.prepare_root()
                after = path.stat()
                self.assertEqual((after.st_gid, after.st_mode, after.st_ctime_ns),
                                 (before.st_gid, before.st_mode, before.st_ctime_ns))
                repaired = {inode for inode, _, _ in owned}
                self.assertNotIn(before.st_ino, repaired)
                self.assertNotIn(fifo.stat().st_ino, repaired)
                self.assertIn(payload.stat().st_ino, repaired)
                self.assertEqual(stat.S_IMODE(payload.stat().st_mode), 0o755)
                with socket.socket(socket.AF_UNIX) as client:
                    client.connect(str(path))
                    connection, _ = server.accept()
                    connection.close()

    def test_hardlinks_are_rejected_before_runtime_repairs(self):
        with tempfile.TemporaryDirectory(prefix="fpk-ipc-", dir="/tmp") as temporary:
            root = Path(temporary)
            cleanup, app = self.fixture(root)
            outside = root / "outside"
            outside.write_text("keep")
            os.link(outside, app / "bad-link")
            with patch.object(runtime, "trusted"), patch.object(runtime.os, "chown") as chown, \
                 patch.dict(os.environ, {"TRIM_APPDEST": str(app)}):
                with self.assertRaises(runtime.Rejected):
                    cleanup.prepare_root()
                chown.assert_not_called()
            self.assertEqual(outside.read_text(), "keep")


if __name__ == "__main__":
    unittest.main()
