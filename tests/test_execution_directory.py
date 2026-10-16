"""Native fixtures execute independently of inherited temporary data paths."""
import os
from pathlib import Path
import shutil
import stat
import subprocess
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

from fnpackkit.execution_directory import execution_directory


class ExecutionDirectoryTests(unittest.TestCase):
    def test_private_executable_directory_ignores_cached_and_environment_tmpdir(self):
        with tempfile.TemporaryDirectory() as inherited:
            previous = os.umask(0o077)
            try:
                with patch.dict(os.environ, {'TMPDIR': inherited}), patch.object(tempfile, 'tempdir', inherited):
                    root = execution_directory()
                self.addCleanup(shutil.rmtree, root)
                self.assertEqual(root.parent, Path('/tmp').resolve())
                self.assertEqual(root.stat().st_uid, os.geteuid())
                self.assertEqual(stat.S_IMODE(root.stat().st_mode), 0o700)
                self.assertLess(len(str(root / 'tmp/org.chromium.Chromium.XXXXXX/SingletonSocket').encode()), 100)
                hook = root / 'hook'
                hook.write_text('#!/bin/sh\nprintf fixture-exec-ok\n')
                hook.chmod(0o700)
                result = subprocess.run([str(hook)], capture_output=True, text=True, check=True)
                self.assertEqual(result.stdout, 'fixture-exec-ok')
            finally:
                os.umask(previous)

    def test_noexec_mount_is_rejected_before_creating_directory(self):
        with patch('fnpackkit.execution_directory.os.statvfs', return_value=SimpleNamespace(f_flag=os.ST_NOEXEC)), \
                patch('fnpackkit.execution_directory.tempfile.mkdtemp') as create:
            with self.assertRaisesRegex(ValueError, 'executable temporary mount.*noexec'):
                execution_directory()
            create.assert_not_called()


if __name__ == '__main__':
    unittest.main()
