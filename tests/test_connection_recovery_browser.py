"""Verify shared recovery with an isolated browser and simulated lifecycle events."""
import json
import os
from pathlib import Path
import subprocess
import unittest

from buildflow.browser import resolve_browser


class ConnectionRecoveryBrowser(unittest.TestCase):
    @unittest.skipUnless(os.environ.get('FNPACKKIT_BROWSER_E2E') == '1',
                         'set FNPACKKIT_BROWSER_E2E=1')
    def test_stalled_channel_recovery_preserves_page_and_repairs_snapshot(self):
        tools = resolve_browser()
        result = subprocess.run(
            [str(tools.node), str(Path(__file__).with_name('connection_recovery_browser.mjs'))],
            env={**os.environ, **tools.environment()}, text=True,
            capture_output=True, timeout=90)
        self.assertEqual(result.returncode, 0, result.stderr)
        report = json.loads(result.stdout)
        self.assertEqual(report['pageErrors'], 0)
        self.assertFalse(report['androidDeviceVerified'])
        self.assertEqual([check['phone'] for check in report['checks']], [False, True])
        for check in report['checks']:
            for name, passed in check.items():
                if name != 'phone':
                    self.assertTrue(passed, name)


if __name__ == '__main__':
    unittest.main()
