"""Regression checks for language-level encoding, adapter and path fixes."""
import ast
from pathlib import Path
import sys
import tempfile
from types import SimpleNamespace
import unittest

from fnpackkit.adapter import load_plan
from fnpackkit.archive import safe_relative

ROOT = Path(__file__).resolve().parents[1]


class TextContractTests(unittest.TestCase):
    def test_python_text_io_declares_utf8(self):
        offenders = []
        for path in (ROOT / 'src/fnpackkit').glob('*.py'):
            tree = ast.parse(path.read_text(encoding='utf-8'))
            for node in ast.walk(tree):
                if not isinstance(node, ast.Call) or not isinstance(node.func, ast.Attribute):
                    continue
                if node.func.attr not in {'read_text', 'write_text'}:
                    continue
                if not any(keyword.arg == 'encoding' for keyword in node.keywords):
                    offenders.append(f'{path.name}:{node.lineno}')
        self.assertEqual(offenders, [])

    def test_safe_relative_rejects_non_text_before_path_construction(self):
        for value in (None, 1, b'bytes', ['list']):
            with self.subTest(value=value), self.assertRaises(ValueError):
                safe_relative(value)


class AdapterIsolationTests(unittest.TestCase):
    def write_adapter(self, root, name):
        path = root / name
        path.write_text(
            'def plan(project):\n'
            '    return {"steps": [], "payload": None, "metadata": {"module": __name__}}\n',
            encoding='utf-8')
        return path

    def test_each_adapter_uses_a_distinct_module_and_restores_sys_path(self):
        with tempfile.TemporaryDirectory(prefix='fnpack-adapter-') as temporary:
            root = Path(temporary)
            first = self.write_adapter(root, 'first.py')
            second = self.write_adapter(root, 'second.py')
            project = SimpleNamespace(root=root)
            before = list(sys.path)
            one = load_plan(project, 'first.py:plan')['metadata']['module']
            two = load_plan(project, 'second.py:plan')['metadata']['module']
            self.assertNotEqual(one, two)
            self.assertEqual(before, sys.path)
            self.assertNotIn(one, sys.modules)
            self.assertNotIn(two, sys.modules)


if __name__ == '__main__':
    unittest.main()
