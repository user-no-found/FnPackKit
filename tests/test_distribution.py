"""Verify complete source handoff and the separate Python wheel contract."""
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import subprocess
import sys
import tarfile
import tempfile
import unittest
import zipfile


@unittest.skipUnless(os.environ.get('FNPACKKIT_DISTRIBUTION_E2E') == '1',
                     'set FNPACKKIT_DISTRIBUTION_E2E=1 for actual source/wheel builds')
class DistributionContract(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.root = Path(__file__).resolve().parents[1]
        selected = os.environ.get('FNPACKKIT_DISTRIBUTION_OUTPUT')
        if selected:
            cls.output = Path(selected).resolve()
            cls.output.mkdir(parents=True, exist_ok=True)
        else:
            temporary = tempfile.TemporaryDirectory(prefix='fnpackkit-dist-')
            cls.addClassCleanup(temporary.cleanup)
            cls.output = Path(temporary.name)
        script = (
            'import json,sys\n'
            'from setuptools import build_meta\n'
            'output=sys.argv[1]\n'
            'source=build_meta.build_sdist(output)\n'
            'wheel=build_meta.build_wheel(output)\n'
            'print(json.dumps({"source":source,"wheel":wheel}))\n'
        )
        result = subprocess.run([sys.executable, '-c', script, str(cls.output)], cwd=cls.root,
                                capture_output=True, text=True, timeout=90)
        if result.returncode:
            raise AssertionError(result.stdout + result.stderr)
        if result.stderr.strip():
            print(result.stderr, file=sys.stderr, end='')
        artifacts = json.loads(result.stdout.splitlines()[-1])
        cls.source = cls.output / artifacts['source']
        cls.wheel = cls.output / artifacts['wheel']

    def test_source_contains_authoritative_interfaces_docs_and_embedded_assets(self):
        expected = {name: self.root / name for name in (
            'AGENTS.md', 'README.md', 'LICENSE', 'pyproject.toml', 'MANIFEST.in', 'build.sh', 'setup.sh')}
        for folder, suffixes in (
                ('src/fnpackkit', {'.py'}), ('runtime', {'.go', '.mod', '.js', '.css'}),
                ('docs', {'.md'}), ('web', {'.js', '.css', '.json'}), ('tests', {'.py', '.mjs'})):
            for path in (self.root / folder).rglob('*'):
                if path.is_file() and path.suffix in suffixes:
                    expected[path.relative_to(self.root).as_posix()] = path
        with tarfile.open(self.source) as archive:
            files = {}
            for member in archive:
                name = PurePosixPath(member.name)
                self.assertFalse(name.is_absolute() or '..' in name.parts, member.name)
                self.assertFalse(member.issym() or member.islnk(), member.name)
                relative = PurePosixPath(*name.parts[1:])
                self.assertFalse(set(relative.parts) & {'tasks', '.git', '.cache', 'output'}, member.name)
                if member.isfile():
                    self.assertNotIn(str(relative), files, member.name)
                    files[str(relative)] = member
            self.assertFalse(set(expected) - set(files), sorted(set(expected) - set(files)))
            for name, source in expected.items():
                with self.subTest(source=name):
                    self.assertEqual(archive.extractfile(files[name]).read(), source.read_bytes())
        digest = hashlib.sha256(self.source.read_bytes()).hexdigest()
        print(f'Source handoff: {self.source} SHA-256 {digest}', flush=True)

    def test_wheel_contains_python_layer_without_parallel_go_sources(self):
        with zipfile.ZipFile(self.wheel) as archive:
            names = set(archive.namelist())
            for path in (self.root / 'src/fnpackkit').glob('*.py'):
                name = 'fnpackkit/' + path.name
                self.assertIn(name, names)
                self.assertEqual(archive.read(name), path.read_bytes())
            self.assertFalse(any(name.endswith('.go') or name.endswith('/go.mod') for name in names))
            licenses = [name for name in names if name.endswith('.dist-info/LICENSE')]
            self.assertEqual(len(licenses), 1)
            self.assertEqual(archive.read(licenses[0]), (self.root / 'LICENSE').read_bytes())


if __name__ == '__main__':
    unittest.main()
