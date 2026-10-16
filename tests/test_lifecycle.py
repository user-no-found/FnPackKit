"""Native lifecycle regressions for availability, diagnostics and custom scripts."""
import io
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import unittest

from fnpackkit.contracts import Lifecycle, LifecycleCommand
from fnpackkit.lifecycle import generate_lifecycle, parse_lifecycle, validate_lifecycle
from fnpackkit.lifecycle_verify import LifecycleFixture


class LifecycleTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='fnpackkit-lifecycle-')
        self.root = Path(self.temp.name)
        self.stage = self.root / 'stage'
        (self.stage / 'app/libexec').mkdir(parents=True)
        (self.stage / 'cmd/hooks').mkdir(parents=True)
        (self.stage / 'manifest').write_text('appname=fixture\nversion=1.0.0\n')
        self.script(self.stage / 'app/libexec/hook', """#!/bin/sh
set -eu
case "$1" in
 install|upgrade) [ -d "$TRIM_APPDEST" ]; mkdir -p "$TRIM_PKGVAR/data";;
 stop) [ -d "$TRIM_APPDEST" ];;
 status) exit 3;;
 fail) exit 7;;
 argument) printf '%s' "$2" > "$TRIM_PKGVAR/argument";;
esac
""")
        self.script(self.stage / 'cmd/hooks/after', """#!/bin/sh
set -eu
[ ! -e "$TRIM_APPDEST" ]
case "${wizard_data_action:-retain}" in
 delete) rm -rf -- "$TRIM_PKGVAR/data";;
 retain) [ -d "$TRIM_PKGVAR/data" ];;
 *) exit 9;;
esac
""")
        self.profile = Lifecycle({
            'install_callback': LifecycleCommand('target', 'libexec/hook', ('install',)),
            'upgrade_init': LifecycleCommand('target', 'libexec/hook', ('stop',)),
            'upgrade_callback': LifecycleCommand('target', 'libexec/hook', ('upgrade',)),
            'uninstall_init': LifecycleCommand('target', 'libexec/hook', ('stop',)),
            'uninstall_callback': LifecycleCommand('package', 'cmd/hooks/after'),
            'main:status': LifecycleCommand('target', 'libexec/hook', ('status',)),
        })

    def tearDown(self):
        self.temp.cleanup()

    @staticmethod
    def script(path, text):
        path.write_text(text)
        path.chmod(0o755)

    def package(self):
        package = self.root / 'fixture.fpk'
        stream = io.BytesIO()
        with tarfile.open(fileobj=stream, mode='w:gz') as inner:
            for path in sorted((self.stage / 'app').rglob('*')):
                inner.add(path, arcname=path.relative_to(self.stage / 'app').as_posix(), recursive=False)
        with tarfile.open(package, mode='w:gz') as outer:
            info = tarfile.TarInfo('app.tgz')
            info.size = len(stream.getvalue())
            outer.addfile(info, io.BytesIO(stream.getvalue()))
            for path in sorted(self.stage.rglob('*')):
                if path == self.stage / 'app' or self.stage / 'app' in path.parents:
                    continue
                outer.add(path, arcname=path.relative_to(self.stage).as_posix(), recursive=False)
        return package

    def model(self):
        root = self.root / 'fixture'
        root.mkdir(mode=0o700)
        return LifecycleFixture(self.package(), root)

    @staticmethod
    def execute(argv, cwd, env):
        return subprocess.run(argv, cwd=cwd, env={'PATH': '/usr/bin:/bin', **env},
                              check=True, capture_output=True, text=True)

    def test_real_archive_install_upgrade_and_uninstall_order(self):
        generate_lifecycle(self.stage, self.profile)
        model = self.model()
        model.install(self.execute)
        (model.root / 'var/data/retained').write_text('bytes')
        model.upgrade(self.execute)
        model.uninstall(self.execute)
        self.assertEqual('bytes', (model.root / 'var/data/retained').read_text())
        model.install(self.execute)
        model.uninstall(self.execute, {'wizard_data_action': 'delete'})
        model.invoke('uninstall_callback', self.execute, {'wizard_data_action': 'delete'})
        self.assertFalse((model.root / 'var/data').exists())
        for event in model.events:
            if event['event'] in ('install_init', 'uninstall_callback'):
                self.assertFalse(event['target_present'])

    def test_old_install_and_uninstall_dispatch_are_detected(self):
        generate_lifecycle(self.stage, self.profile)
        for phase in ('install_init', 'uninstall_callback'):
            with self.subTest(phase=phase):
                model_root = self.root / ('fixture-' + phase)
                model_root.mkdir(mode=0o700)
                model = LifecycleFixture(self.package(), model_root)
                self.script(model.metadata / 'cmd' / phase,
                            '#!/bin/sh\nexec "$TRIM_APPDEST/libexec/hook" stop\n')
                if phase == 'install_init':
                    with self.assertRaises(subprocess.CalledProcessError):
                        model.install(self.execute)
                    self.assertFalse(model.app.exists())
                else:
                    model.install(self.execute)
                    with self.assertRaises(subprocess.CalledProcessError):
                        model.uninstall(self.execute)
                    self.assertFalse(model.app.exists())

    def test_diagnostics_keep_failure_code_and_never_print_values(self):
        self.profile.hooks['config_init'] = LifecycleCommand(
            'target', 'libexec/hook', ('fail',), ('TRIM_UID',))
        generate_lifecycle(self.stage, self.profile)
        model = self.model()
        model.install(self.execute)
        with self.assertRaises(subprocess.CalledProcessError) as caught:
            model.invoke('config_init', self.execute)
        self.assertEqual(1, caught.exception.returncode)
        log = model.root / 'callback-error.log'
        self.assertIn('missing required TRIM_UID', log.read_text())
        secret = 'private-value-do-not-display'
        with self.assertRaises(subprocess.CalledProcessError) as caught:
            model.invoke('config_init', self.execute, {'TRIM_UID': secret})
        self.assertEqual(7, caught.exception.returncode)
        self.assertIn('config_init: command failed (exit 7)', log.read_text())
        self.assertNotIn(secret, log.read_text())
        (model.app / 'libexec/hook').unlink()
        with self.assertRaises(subprocess.CalledProcessError):
            model.invoke('install_callback', self.execute)
        self.assertIn('required command missing', log.read_text())

    def test_status_and_literal_arguments(self):
        literal = '$(touch forbidden) ; space "quote"'
        self.profile.hooks['config_callback'] = LifecycleCommand(
            'target', 'libexec/hook', ('argument', literal))
        generate_lifecycle(self.stage, self.profile)
        model = self.model()
        model.install(self.execute)
        model.invoke('config_callback', self.execute)
        self.assertEqual(literal, (model.root / 'var/argument').read_text())
        self.assertFalse((model.root / 'forbidden').exists())
        with self.assertRaises(subprocess.CalledProcessError) as caught:
            model.invoke('main:status', self.execute)
        self.assertEqual(3, caught.exception.returncode)
        self.assertFalse((model.root / 'callback-error.log').exists())

    def test_custom_scripts_unchanged_and_opt_in_conflicts_rejected(self):
        path = self.stage / 'cmd/install_init'
        self.script(path, '#!/bin/sh\nexit 0\n')
        before = path.read_bytes()
        generate_lifecycle(self.stage, None)
        self.assertEqual(before, path.read_bytes())
        with self.assertRaisesRegex(ValueError, 'overwrite custom'):
            generate_lifecycle(self.stage, self.profile)
        self.assertEqual(before, path.read_bytes())

    def test_declaration_and_fixture_boundaries(self):
        for phase in ('install_init', 'uninstall_callback'):
            with self.assertRaisesRegex(ValueError, 'cannot depend on target'):
                validate_lifecycle(Lifecycle({phase: LifecycleCommand('target', 'libexec/hook')}))
        for value in [
            {'hooks': {'unknown': {'location': 'target', 'path': 'libexec/hook'}}},
            {'hooks': {'install_callback': {'location': 'package', 'path': '../outside'}}},
            {'hooks': {'install_callback': {'location': 'package', 'path': 'app/file'}}},
            {'hooks': {'install_callback': {'location': 'target', 'path': 'libexec/hook', 'args': 'bad'}}},
            {'hooks': {'install_callback': {'location': 'target', 'path': 'libexec/hook', 'required_env': ['BAD']}}},
            {'hooks': {}, 'unexpected': True},
        ]:
            with self.subTest(value=value), self.assertRaises(ValueError):
                parse_lifecycle(value)
        generate_lifecycle(self.stage, self.profile)
        model = self.model()
        with self.assertRaisesRegex(ValueError, 'override'):
            model.invoke('install_init', self.execute, {'TRIM_APPDEST': '/var/apps/tuios/target'})
        with self.assertRaisesRegex(ValueError, 'empty private'):
            LifecycleFixture(model.package, model.root)
        link = self.root / 'linked-target'
        link.mkdir()
        model.app.symlink_to(link)
        with self.assertRaisesRegex(ValueError, 'unsafe fixture'):
            model.remove_target()

    def test_fntuios_successful_pre_and_post_entries(self):
        reference = Path(__file__).resolve().parents[2] / 'FnTuiOS/packaging/fnos/cmd'
        if not reference.is_dir():
            self.skipTest('FnTuiOS reference checkout unavailable')
        generate_lifecycle(self.stage, self.profile)
        for name in ('install_init', 'uninstall_callback'):
            shutil.copyfile(reference / name, self.stage / 'cmd' / name)
            (self.stage / 'cmd' / name).chmod(0o755)
        model = self.model()
        model.install(self.execute)
        model.uninstall(self.execute)
        self.assertTrue((model.root / 'var/data').is_dir())


if __name__ == '__main__':
    unittest.main()
