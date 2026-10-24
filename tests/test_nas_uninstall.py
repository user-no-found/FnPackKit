"""Explicit NAS-UID state policies preserve root authority and bound cleanup."""
from dataclasses import replace
import os
from pathlib import Path
import pwd
import shutil
import unittest

from fnpackkit.contracts import Lifecycle, LifecycleCommand, UninstallPolicy
from fnpackkit.lifecycle import generate_lifecycle
from fnpackkit.uninstall import generate_uninstall, validate_uninstall
from tests import test_uninstall as package_tests
from tests.test_uninstall import policy


class NASDeclarations(unittest.TestCase):
    def test_roots_are_explicit_and_package_traversal_cannot_enter_them(self):
        validate_uninstall(replace(policy(), transient=(), nas_user_roots=('var', 'tmp')))
        for roots in (('etc',), ('var', 'var'), ['var']):
            with self.assertRaises(ValueError):
                validate_uninstall(replace(policy(), transient=(), nas_user_roots=roots))
        with self.assertRaises(ValueError):
            validate_uninstall(replace(policy(), nas_user_roots=('var',)))


@unittest.skipUnless(os.geteuid() == 0 and os.environ.get('FNP_TEST_USERNAME')
                     and os.environ.get('FNP_TEST_NAS_USERNAME'),
                     'requires explicitly selected package/NAS accounts and root fixture')
class NASCleanup(unittest.TestCase):
    def setUp(self):
        package_tests.NativeCleanupTests.setUp(self)
        self.nas = pwd.getpwnam(os.environ['FNP_TEST_NAS_USERNAME'])
        assert self.nas.pw_uid >= 1000
        for root in self.roots.values():
            shutil.rmtree(root)
            root.mkdir(mode=0o700)
        shutil.rmtree(self.stage / 'cmd')
        shutil.rmtree(self.stage / 'wizard')
        self.lifecycle = Lifecycle({'uninstall_init': LifecycleCommand(
            'target', 'libexec/hook', ('stop',), run_as='current')})
        generate_lifecycle(self.stage, self.lifecycle)
        generate_uninstall(self.stage, UninstallPolicy(
            ('etc/settings', 'var/*'), (), 'NAS data', 'Data', 'Retain', 'Delete',
            nas_user_roots=('var', 'tmp')), self.app_id, self.lifecycle)
        for label in ('var', 'tmp'):
            path = self.roots[label] / str(self.nas.pw_uid)
            path.mkdir(mode=0o700)
            (path / 'private').write_text('synthetic private NAS state')
            os.chown(path / 'private', self.nas.pw_uid, self.nas.pw_gid)
            (path / 'private').chmod(0o600)
            os.chown(path, self.nas.pw_uid, self.nas.pw_gid)
        (self.roots['etc'] / 'settings').write_text('root authority')
        (self.roots['etc'] / 'settings').chmod(0o600)
        self.env.update(TRIM_RUN_UID='0', TRIM_RUN_GID='0')

    write = package_tests.NativeCleanupTests.write
    tearDown = package_tests.NativeCleanupTests.tearDown
    run_phase = package_tests.NativeCleanupTests.run_phase
    uninstall = package_tests.NativeCleanupTests.uninstall

    def test_prepare_never_hands_authority_or_state_to_package_user(self):
        result = self.run_phase('install_callback')
        self.assertEqual(result.returncode, 0, result.stderr)
        for root in self.roots.values():
            self.assertEqual(root.stat().st_uid, 0)
        private = self.roots['var'] / str(self.nas.pw_uid) / 'private'
        self.assertEqual(private.stat().st_uid, self.nas.pw_uid)
        self.assertEqual(private.read_text(), 'synthetic private NAS state')
        self.assertEqual((self.roots['etc'] / 'settings').stat().st_uid, 0)
        self.uninstall()
        self.assertTrue(private.is_file())
        self.assertFalse(self.roots['tmp'].exists())
        self.assertTrue((self.roots['var'] / 'events').read_text().startswith('stop:0:0:0:0'))

    def test_delete_removes_only_declared_application_state(self):
        outside = self.root / 'outside'
        outside.write_text('keep outside')
        link = self.roots['var'] / str(self.nas.pw_uid) / 'link'
        link.symlink_to(outside)
        os.lchown(link, self.nas.pw_uid, self.nas.pw_gid)
        self.uninstall(wizard_data_action='delete')
        self.assertTrue(all(not path.exists() for path in self.roots.values()))
        self.assertEqual(outside.read_text(), 'keep outside')

    def test_foreign_ownership_and_unsafe_uid_root_fail_before_stop(self):
        private = self.roots['var'] / str(self.nas.pw_uid) / 'private'
        os.chown(private, 65534, 65534)
        result = self.run_phase('uninstall_init', wizard_data_action='delete')
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.roots['var'] / 'events').exists())
        self.assertTrue(private.exists())
        os.chown(private, self.nas.pw_uid, self.nas.pw_gid)
        private.parent.chmod(0o777)
        result = self.run_phase('uninstall_init', wizard_data_action='delete')
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.roots['var'] / 'events').exists())

    def test_package_account_hooks_rejected_for_nas_authority(self):
        shutil.rmtree(self.stage / 'cmd')
        lifecycle = Lifecycle({'uninstall_init': LifecycleCommand(
            'target', 'libexec/hook', ('stop',), run_as='package')})
        generate_lifecycle(self.stage, lifecycle)
        with self.assertRaises(ValueError):
            generate_uninstall(self.stage, replace(policy(), transient=(), nas_user_roots=('var',)),
                               self.app_id, lifecycle)


@unittest.skipUnless(os.geteuid() != 0, 'fixture construction uses current nonroot identity')
class FixtureConstruction(unittest.TestCase):
    def test_private_umask_keeps_program_ancestors_traversable_and_wizard_unique(self):
        from unittest.mock import patch
        account = pwd.getpwuid(os.geteuid())
        with patch.dict(os.environ, {'FNP_TEST_USERNAME': account.pw_name,
                                     'FNP_TEST_NAS_USERNAME': account.pw_name}):
            previous = os.umask(0o077)
            try:
                for kind in (package_tests.NativeCleanupTests, NASCleanup):
                    case = kind(methodName='runTest')
                    case.setUp()
                    try:
                        for path in (case.volume, case.app.parent, case.app, case.app / 'libexec'):
                            self.assertEqual(path.stat().st_mode & 0o777, 0o755, str(path))
                        self.assertTrue((case.stage / 'wizard/uninstall').is_file())
                    finally:
                        case.tearDown()
            finally:
                os.umask(previous)
