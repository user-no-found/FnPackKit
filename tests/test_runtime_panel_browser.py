"""Exercise shared runtime control presentation with the registered real browser."""
import json
import os
from pathlib import Path
import subprocess
import unittest

from buildflow.browser import resolve_browser


class RuntimePanelBrowser(unittest.TestCase):
    @unittest.skipUnless(os.environ.get('FNPACKKIT_BROWSER_E2E') == '1', 'set FNPACKKIT_BROWSER_E2E=1')
    def test_controls_drag_docking_and_stopped_reload(self):
        tools = resolve_browser()
        result = subprocess.run([str(tools.node), str(Path(__file__).with_name('browser_runtime_panel.mjs'))],
                                env={**os.environ, **tools.environment()}, text=True,
                                capture_output=True, timeout=90)
        self.assertEqual(result.returncode, 0, result.stderr)
        report = json.loads(result.stdout)
        self.assertEqual(report['pageErrors'], 0)
        self.assertGreaterEqual(len(report['checks']), 9)

    @unittest.skipUnless(os.environ.get('FNPACKKIT_BROWSER_E2E') == '1', 'set FNPACKKIT_BROWSER_E2E=1')
    def test_authorization_password_dom_lifecycle(self):
        tools = resolve_browser()
        result = subprocess.run([str(tools.node), str(Path(__file__).with_name('browser_control_authorization.mjs'))],
                                env={**os.environ, **tools.environment()}, text=True,
                                capture_output=True, timeout=120)
        self.assertEqual(result.returncode, 0, result.stderr)
        report = json.loads(result.stdout)
        self.assertEqual(report['pageErrors'], 0)
        self.assertFalse(report['nativeAutofillVerified'])
        self.assertEqual([check['phone'] for check in report['checks']], [False, True])
        for check in report['checks']:
            for name, passed in check.items():
                if name not in ('phone', 'compactKeyboard'):
                    self.assertTrue(passed, name)
        self.assertTrue(report['checks'][1]['compactKeyboard'])


if __name__ == '__main__':
    unittest.main()
