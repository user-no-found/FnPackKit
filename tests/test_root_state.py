"""Explicit root-owned bridge state retains the shared confinement checks."""
import dataclasses
import os
import shutil
import socket
import stat
import unittest

from fnpackkit.contracts import Lifecycle, LifecycleCommand
from fnpackkit.lifecycle import generate_lifecycle
from fnpackkit.uninstall import generate_uninstall, validate_uninstall
import test_uninstall as package_tests


class RootStateDeclarations(unittest.TestCase):
    def test_identity_is_explicit_and_cannot_mix_nas_trees(self):
        validate_uninstall(dataclasses.replace(package_tests.policy(), state_owner="root"))
        for values in ({"state_owner": "other"}, {"state_owner": "root", "nas_user_roots": ("var",)}):
            with self.assertRaises(ValueError):
                validate_uninstall(dataclasses.replace(package_tests.policy(), **values))


@unittest.skipUnless(os.geteuid() == 0 and os.environ.get("FNP_TEST_USERNAME"), "isolated root fixture only")
class RootStateOwnership(unittest.TestCase):
    def fixture(self):
        case = package_tests.NativeCleanupTests("test_retain_keeps_vault_and_config_prunes_transients_and_drops_root")
        case.setUp()
        self.addCleanup(case.tearDown)
        shutil.rmtree(case.stage / "cmd")
        shutil.rmtree(case.stage / "wizard")
        (case.stage / "cmd").mkdir()
        hook = '#!/bin/sh\nset -eu\nprintf "stop:%s\\n" "$(id -u)" >> "$TRIM_PKGVAR/events"\n'
        for path in (case.stage / "app/libexec/hook", case.app / "libexec/hook"):
            path.write_text(hook)
            path.chmod(0o755)
        lifecycle = Lifecycle({"uninstall_init": LifecycleCommand("target", "libexec/hook", ("stop",), run_as="current")})
        generate_lifecycle(case.stage, lifecycle)
        generate_uninstall(case.stage, dataclasses.replace(package_tests.policy(), state_owner="root"), case.app_id, lifecycle)
        return case

    def test_prepare_retain_and_delete_preserve_external_data(self):
        for action in ("retain", "delete"):
            with self.subTest(action=action):
                case = self.fixture()
                outside = case.root / "native-data"
                outside.write_text("synthetic native data")
                result = case.run_phase("install_callback")
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(case.app.stat().st_uid, 0)
                for root in case.roots.values():
                    self.assertEqual(root.stat().st_uid, 0)
                    self.assertEqual(root.stat().st_mode & 0o777, 0o700)
                case.uninstall(wizard_data_action=action)
                self.assertEqual(outside.read_text(), "synthetic native data")
                if action == "retain":
                    self.assertEqual((case.roots["var"] / "events").read_text(), "stop:0\n")
                    self.assertEqual((case.roots["var"] / "vault/database").read_text(), "synthetic database")
                else:
                    self.assertTrue(all(not path.exists() for path in case.roots.values()))

    def test_root_prepare_rejects_hardlinks_before_changing_ownership(self):
        case = self.fixture()
        outside = case.root / "external"
        outside.write_text("keep")
        os.link(outside, case.roots["var"] / "unsafe-link")
        result = case.run_phase("install_callback")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("hardlinked_entry", result.stderr)
        self.assertEqual(case.roots["var"].stat().st_uid, case.account.pw_uid)
        self.assertEqual(outside.read_text(), "keep")

    def test_prepare_restores_private_state_and_nonwritable_payload(self):
        case=self.fixture()
        private=case.roots["var"] / "vault/key"
        private.chmod(0o666)
        payload=case.app / "libexec/reference.json"
        payload.write_text('{}\n');payload.chmod(0o664)
        result=case.run_phase("install_callback")
        self.assertEqual(result.returncode,0,result.stderr)
        self.assertEqual(private.stat().st_mode & 0o777,0o600)
        self.assertEqual(payload.stat().st_mode & 0o777,0o644)

    def test_root_prepare_preserves_active_runtime_socket_group(self):
        case = self.fixture()
        path = case.app / "backend.sock"
        with socket.socket(socket.AF_UNIX) as server:
            server.bind(str(path))
            server.listen(1)
            os.chmod(path, 0o660)
            os.chown(path, 0, case.account.pw_gid)
            for _ in range(3):
                result = case.run_phase("config_init")
                self.assertEqual(result.returncode, 0, result.stderr)
                info = path.stat()
                self.assertEqual((info.st_uid, info.st_gid, stat.S_IMODE(info.st_mode)),
                                 (0, case.account.pw_gid, 0o660))
