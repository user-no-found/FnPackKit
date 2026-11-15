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
                                capture_output=True, timeout=180)
        self.assertEqual(result.returncode, 0, result.stderr)
        report = json.loads(result.stdout)
        self.assertEqual(report['pageErrors'], 0)
        self.assertGreaterEqual(len(report['checks']), 16)
        self.assertEqual([item['input'] for item in report['realDeadlines']], ['mouse', 'touch'])
        for timing in report['realDeadlines']:
            self.assertGreaterEqual(timing['revealMs'], 3000)
            self.assertGreaterEqual(timing['openMs'], 5000)
        self.assertIn('Playwright', report['preciseTiming'])
        self.assertIn('CDP Input.dispatchTouchEvent', report['touchInput'])
        self.assertIn('not fnOS Android hardware', report['deviceBoundary'])
        self.assertEqual([command['action'] for command in report['controls']],
                         ['stop', 'start', 'restart', 'stop'])
        self.assertTrue(all(command.get('confirm') for command in report['controls']
                            if command['action'] in ('stop', 'restart')))

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
        evidence = os.environ.get('FNPACKKIT_RUNTIME_EVIDENCE')
        if evidence:
            directory = Path(evidence)
            directory.mkdir(parents=True, exist_ok=True)
            (directory / 'credentials-report.json').write_text(
                json.dumps(report, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')


if __name__ == '__main__':
    unittest.main()
