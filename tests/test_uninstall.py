"""Root-owned synthetic volume tests for namespace confinement and lifecycle identity."""
import importlib.util
import json
import os
from pathlib import Path
import pwd
import shutil
import socket
import subprocess
import tempfile
import unittest

from fnpackkit.contracts import Lifecycle, LifecycleCommand, UninstallPolicy
from fnpackkit.lifecycle import generate_lifecycle
from fnpackkit.uninstall import generate_uninstall, parse_uninstall, validate_uninstall


def policy():
    return UninstallPolicy(('etc/settings', 'var/vault/database', 'var/vault/key'),
                           ('var/cache', 'var/users/*/tmp'),
                           '卸载数据', '测试数据', '保留数据', '永久删除数据')


class DeclarationTests(unittest.TestCase):
    def test_protected_patterns_cannot_be_pruned(self):
        valid = policy()
        validate_uninstall(valid)
        for path in ('var', '../var/cache', 'var/../cache', '/var/cache',
                     'var/a**/cache', 'var/vault', 'var/*/database'):
            with self.subTest(path=path), self.assertRaises(ValueError):
                validate_uninstall(UninstallPolicy(valid.protected, (path,),
                    valid.title, valid.label, valid.retain_label, valid.delete_label))

    def test_json_round_trip_and_unknown_fields(self):
        data = dict(policy().__dict__)
        data.update(protected=list(data['protected']), transient=list(data['transient']))
        self.assertEqual(parse_uninstall(data), policy())
        with self.assertRaises(ValueError):
            parse_uninstall({**data, 'delete_external': True})


@unittest.skipUnless(os.geteuid() == 0 and os.environ.get('FNP_TEST_USERNAME'),
                     'requires explicitly selected nonroot account and synthetic root volume')
class NativeCleanupTests(unittest.TestCase):
    def setUp(self):
        self.account = pwd.getpwnam(os.environ['FNP_TEST_USERNAME'])
        self.assertNotEqual(self.account.pw_uid, 0)
        self.temp = tempfile.TemporaryDirectory(prefix='fnpack-policy-test-')
        self.root = Path(self.temp.name)
        self.root.chmod(0o711)
        self.stage = self.root / 'package'
        (self.stage / 'cmd/hooks').mkdir(parents=True)
        (self.stage / 'app/libexec').mkdir(parents=True)
        (self.stage / 'config').mkdir()
        (self.stage / 'config/privilege').write_text('{"defaults":{"run-as":"root"}}')
        self.volume = self.root / 'volume'
        self.volume.mkdir()
        self.app_id = 'fnpack-fixture'
        self.roots = {}
        dirs = {'etc': ('TRIM_PKGETC', '@appconf'), 'var': ('TRIM_PKGVAR', '@appdata'),
                'home': ('TRIM_PKGHOME', '@apphome'), 'meta': ('TRIM_PKGMETA', '@appmeta'),
                'tmp': ('TRIM_PKGTMP', '@apptemp')}
        self.env = {'PATH': '/usr/bin:/bin', 'TRIM_APPNAME': self.app_id,
                    'TRIM_APPDEST_VOL': str(self.volume), 'TRIM_USERNAME': self.account.pw_name,
                    'TRIM_UID': str(self.account.pw_uid),
                    'TRIM_GID': os.environ.get('FNP_TEST_GID', str(self.account.pw_gid)),
                    'TRIM_TEMP_LOGFILE': str(self.root / 'error.log')}
        for name, (var, namespace) in dirs.items():
            parent = self.volume / namespace
            parent.mkdir()
            path = parent / self.app_id
            path.mkdir(mode=0o700)
            os.chown(path, self.account.pw_uid, self.account.pw_gid)
            self.roots[name] = path
            self.env[var] = str(path)
        parent = self.volume / '@appcenter'
        parent.mkdir()
        self.app = parent / self.app_id
        self.env['TRIM_APPDEST'] = str(self.app)
        hook = self.stage / 'app/libexec/hook'
        hook.write_text('''#!/bin/sh
set -eu
printf '%s:%s:%s:%s:%s\\n' "$1" "$(id -u)" "$TRIM_RUN_UID" "$(id -g)" "$TRIM_RUN_GID" >> "$TRIM_PKGVAR/events"
''')
        hook.chmod(0o755)
        self.lifecycle = Lifecycle({'uninstall_init': LifecycleCommand(
            'target', 'libexec/hook', ('stop',), run_as='package')})
        generate_lifecycle(self.stage, self.lifecycle)
        generate_uninstall(self.stage, policy(), self.app_id, self.lifecycle)
        shutil.copytree(self.stage / 'app', self.app)
        self.write(self.roots['etc'] / 'settings', 'configuration')
        self.write(self.roots['var'] / 'vault/database', 'synthetic database')
        self.write(self.roots['var'] / 'vault/key', 'synthetic key')
        self.write(self.roots['var'] / 'cache/compiled', 'disposable cache')
        self.write(self.roots['var'] / 'users/123/tmp/session', 'disposable session')
        self.write(self.roots['var'] / 'users/123/key', 'kept user key')
        self.write(self.roots['tmp'] / 'state', 'disposable run state')

    def write(self, path, value):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(value)
        for p in [path, *path.parents]:
            if p == self.volume or p.parent == self.volume:
                break
            os.chown(p, self.account.pw_uid, self.account.pw_gid)

    def tearDown(self):
        self.temp.cleanup()

    def run_phase(self, phase, **extra):
        return subprocess.run([str(self.stage / 'cmd' / phase)], env={**self.env, **extra},
                              capture_output=True, text=True)

    def uninstall(self, **env):
        result = self.run_phase('uninstall_init', **env)
        self.assertEqual(result.returncode, 0, result.stderr)
        shutil.rmtree(self.app)
        result = self.run_phase('uninstall_callback', **env)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_retain_keeps_vault_and_config_prunes_transients_and_drops_root(self):
        self.uninstall()
        self.assertEqual((self.roots['var'] / 'vault/database').read_text(), 'synthetic database')
        self.assertEqual((self.roots['var'] / 'vault/key').read_text(), 'synthetic key')
        self.assertEqual((self.roots['etc'] / 'settings').read_text(), 'configuration')
        self.assertEqual((self.roots['var'] / 'users/123/key').read_text(), 'kept user key')
        uid = self.account.pw_uid
        gid = self.env['TRIM_GID']
        self.assertEqual((self.roots['var'] / 'events').read_text(), f'stop:{uid}:{uid}:{gid}:{gid}\n')
        for path in (self.roots['var'] / 'cache', self.roots['var'] / 'users/123/tmp',
                     self.roots['home'], self.roots['meta'], self.roots['tmp']):
            self.assertFalse(path.exists())
        self.assertEqual(self.run_phase('uninstall_callback').returncode, 0)

    def test_delete_removes_all_roots_socket_symlink_and_repeats(self):
        outside = self.root / 'outside'
        outside.write_text('unchanged')
        (self.roots['var'] / 'outside-link').symlink_to(outside)
        sock = socket.socket(socket.AF_UNIX)
        sock.bind(str(self.roots['var'] / 'orphan.sock'))
        sock.close()
        self.uninstall(wizard_data_action='delete')
        self.assertEqual(outside.read_text(), 'unchanged')
        self.assertTrue(all(not p.exists() for p in self.roots.values()))
        self.assertEqual(self.run_phase('uninstall_callback', wizard_data_action='delete').returncode, 0)

    def assert_rejected_without_mutation(self, **env):
        result = self.run_phase('uninstall_init', wizard_data_action='delete', **env)
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(self.app.is_dir())
        self.assertTrue((self.roots['var'] / 'vault/database').is_file())
        self.assertFalse((self.roots['var'] / 'events').exists())
        self.assertNotIn('synthetic key', result.stderr)

    def test_identity_and_path_mismatch_fail_before_stop(self):
        for env in ({'TRIM_APPNAME': 'another-app'}, {'TRIM_UID': '0'},
                    {'TRIM_PKGETC': str(self.root)}, {'TRIM_APPDEST_VOL': '/'},
                    {'TRIM_USERNAME': 'root'}, {'TRIM_APPDEST': str(self.root)}, {'TRIM_GID': '0'}):
            with self.subTest(env=env):
                self.assert_rejected_without_mutation(**env)
        result = self.run_phase('uninstall_init', wizard_data_action='wrong')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('invalid_data_action', (self.root / 'error.log').read_text())

    def test_root_symlink_and_unsafe_parent_never_follow(self):
        root = self.roots['home']
        root.rmdir()
        root.symlink_to(self.root, target_is_directory=True)
        self.assert_rejected_without_mutation()
        root.unlink()
        root.mkdir()
        os.chown(root, self.account.pw_uid, self.account.pw_gid)
        root.parent.chmod(0o777)
        self.assert_rejected_without_mutation()

    def test_hardlink_and_foreign_owner_are_rejected_preflight(self):
        target = self.roots['meta'] / 'hardlink'
        os.link(self.roots['var'] / 'vault/key', target)
        self.assert_rejected_without_mutation()
        target.unlink()
        target.write_text('foreign synthetic entry')
        os.chown(target, 65534, 65534)
        self.assert_rejected_without_mutation()

    def test_symlink_in_transient_parent_is_rejected_before_pruning(self):
        user = self.roots['var'] / 'users/456'
        user.symlink_to(self.root, target_is_directory=True)
        result = self.run_phase('uninstall_init')
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue((self.roots['var'] / 'cache/compiled').is_file())
        self.assertFalse((self.roots['var'] / 'events').exists())

    def test_mount_descendant_is_rejected_including_same_device_bind(self):
        file = self.stage / 'cmd/hooks/fnpack_uninstall.py'
        spec = importlib.util.spec_from_file_location('cleanup_fixture', file)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        from unittest.mock import patch
        import contextlib
        with patch.dict(os.environ, self.env, clear=True), patch.object(
                module, 'mountpoints', return_value={str(self.roots['var'] / 'cache')}):
            with contextlib.ExitStack() as stack, self.assertRaisesRegex(module.Rejected, 'mounted'):
                module.Cleanup(stack)

    def test_platform_root_ownership_is_prepared_and_post_cleanup_accepts_root(self):
        sock = socket.socket(socket.AF_UNIX)
        socket_path = self.roots['var'] / 'cache/orphan.sock'
        sock.bind(str(socket_path))
        sock.close()
        for path in self.roots.values():
            os.chown(path, 0, 0)
            path.chmod(0o770)
            for child in path.rglob('*'):
                os.chown(child, 0, 0)
        result = self.run_phase('install_callback')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.app.stat().st_uid, self.account.pw_uid)
        self.assertEqual((self.roots['var'] / 'cache/compiled').stat().st_uid, self.account.pw_uid)
        self.assertEqual(socket_path.stat().st_uid, self.account.pw_uid)
        for path in self.roots.values():
            self.assertEqual(path.stat().st_uid, self.account.pw_uid)
            self.assertEqual(path.stat().st_mode & 0o777, 0o700)
        for path in self.roots.values():
            os.chown(path, 0, 0)
        self.uninstall(wizard_data_action='delete')
        self.assertTrue(all(not p.exists() for p in self.roots.values()))


if __name__ == '__main__':
    unittest.main()
