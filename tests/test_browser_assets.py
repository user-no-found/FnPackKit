"""Verify the browser bridge's package boundary without live registry access."""
import base64
import hashlib
import importlib.util
import io
import json
import os
import subprocess
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch

from fnpackkit import browser_assets
from buildflow.browser import resolve_browser


class BrowserAssets(unittest.TestCase):
    def source_fixture(self, root):
        web = root / 'web'
        web.mkdir(parents=True)
        (root / 'runtime').mkdir()
        (root / 'runtime/go.mod').write_text('module github.com/user-no-found/FnPackKit/runtime\n')
        (root / 'src/fnpackkit').mkdir(parents=True)
        (root / 'src/fnpackkit/__init__.py').write_text('')
        (root / 'pyproject.toml').write_text('[project]\nname = "fnpackkit"\n')
        (web / 'external-links.js').write_text('export const shared = true;')
        archive = io.BytesIO()
        with tarfile.open(fileobj=archive, mode='w:gz') as package:
            for name, text in {
                'dist/index.js': 'export class TrimApp {}',
                'package.json': json.dumps({'name': '@trimjs/web-app', 'version': '0.4.2'}),
                'README.md': 'SDK notice',
            }.items():
                value = text.encode()
                info = tarfile.TarInfo('package/' + name)
                info.size = len(value)
                package.addfile(info, io.BytesIO(value))
        data = archive.getvalue()
        (web / 'sdk.lock.json').write_text(json.dumps({
            'package': '@trimjs/web-app', 'version': '0.4.2', 'url': 'https://registry.invalid/sdk',
            'integrity': 'sha512-' + base64.b64encode(hashlib.sha512(data).digest()).decode(),
        }))
        cache = root / 'cache'
        cache.mkdir()
        cached = cache / 'trimjs-web-app-0.4.2.tgz'
        cached.write_bytes(data)
        index = root / 'index.html'
        index.write_text('<html><head><script type="module" src="app.js"></script></head></html>')
        labels = {key: key for key in ('title', 'copied', 'copyFailed', 'copy', 'open', 'close')}
        return index, cache, labels, cached

    def install(self, module, fixture):
        with patch.object(module, 'urlopen', side_effect=AssertionError('unexpected SDK download')):
            module.install_external_links(*fixture[:3])

    def load_module(self, path):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(Path(browser_assets.__file__).read_text())
        spec = importlib.util.spec_from_file_location('_browser_assets_fixture', path)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        return module

    def test_verified_sdk_precedes_upstream_module_and_staging_is_idempotent(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            fixture = self.source_fixture(root)
            index, _, _, cached = fixture
            with patch.dict(os.environ, {'FNPACKKIT_PATH': str(root)}):
                self.install(browser_assets, fixture)
                self.install(browser_assets, fixture)
                self.assertEqual(index.read_text().count(browser_assets.MARKER), 1)
                self.assertLess(index.read_text().index(browser_assets.MARKER), index.read_text().index('src="app.js"'))
                self.assertEqual((root / 'fnpackkit/sdk.js').read_text(), 'export class TrimApp {}')
                cached.write_bytes(b'tampered')
                with self.assertRaisesRegex(ValueError, 'integrity mismatch'):
                    self.install(browser_assets, fixture)

    def test_explicit_invalid_source_never_falls_back_to_checkout(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            fixture = self.source_fixture(root)
            incomplete = root / 'incomplete'
            incomplete.mkdir()
            for value in ('', str(root / 'missing'), str(incomplete), str(fixture[0])):
                with self.subTest(value=value), patch.dict(os.environ, {'FNPACKKIT_PATH': value}):
                    with self.assertRaisesRegex(ValueError, 'FNPACKKIT_PATH.*complete FnPackKit source'):
                        self.install(browser_assets, fixture)
            self.assertFalse((root / 'fnpackkit').exists())

    def test_source_checkout_without_explicit_environment(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            fixture = self.source_fixture(root)
            module = self.load_module(root / 'src/fnpackkit/browser_assets.py')
            with patch.dict(os.environ):
                os.environ.pop('FNPACKKIT_PATH', None)
                self.install(module, fixture)
            self.assertEqual((root / 'fnpackkit/external-links.js').read_text(), 'export const shared = true;')

    def test_wheel_layout_uses_selected_sources_and_explains_missing_sources(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            sources = root / 'sources'
            fixture = self.source_fixture(sources)
            module = self.load_module(root / 'venv/lib/python3.11/site-packages/fnpackkit/browser_assets.py')
            with patch.dict(os.environ):
                os.environ.pop('FNPACKKIT_PATH', None)
                with self.assertRaisesRegex(ValueError, 'FNPACKKIT_PATH.*complete FnPackKit source'):
                    self.install(module, fixture)
            with patch.dict(os.environ, {'FNPACKKIT_PATH': str(sources)}):
                self.install(module, fixture)
            self.assertEqual((sources / 'fnpackkit/sdk.js').read_text(), 'export class TrimApp {}')

    @unittest.skipUnless(os.environ.get('FNPACKKIT_BROWSER_E2E') == '1', 'set FNPACKKIT_BROWSER_E2E=1')
    def test_external_navigation_uses_registered_shared_browser(self):
        tools = resolve_browser()
        script = Path(__file__).with_name('browser_external_links.mjs')
        result = subprocess.run([str(tools.node), str(script)],
                                env={**os.environ, **tools.environment()}, capture_output=True,
                                text=True, timeout=45)
        self.assertEqual(result.returncode, 0, result.stderr)
        report = json.loads(result.stdout)
        self.assertEqual(report['pageErrors'], 0)
        self.assertGreaterEqual(len(report['checks']), 6)


if __name__ == '__main__':
    unittest.main()
