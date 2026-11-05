"""Atomic replacement and exhaustive rollback under persistent filesystem failures."""
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from fnpackkit.optional_transaction import ProjectionRollbackError, commit, state
from fnpackkit import optional_transaction


class OptionalTransactionTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="fnpackkit-transaction-")
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        for name, mode in (("a.txt", 0o644), ("z.txt", 0o444)):
            path = self.root / name
            path.write_bytes(name.encode())
            path.chmod(mode)
        self.before = {name: state(self.root, name) for name in ("a.txt", "z.txt")}
        self.after = {name: (b"changed " + value[0], value[1]) for name, value in self.before.items()}

    def assert_original(self):
        self.assertEqual({name: state(self.root, name) for name in self.before}, self.before)
        self.assertEqual({path.name for path in self.root.iterdir()}, set(self.before))

    def test_atomic_replace_can_change_read_only_file_with_writable_parent(self):
        commit(self.root, self.before, self.after)
        self.assertEqual({name: state(self.root, name) for name in self.before}, self.after)
        self.assertEqual({path.name for path in self.root.iterdir()}, set(self.before))

    def test_persistent_replace_failure_preserves_failed_path_and_restores_prior_file(self):
        replace = os.replace
        attempted = []

        def persistent_failure(source, destination):
            attempted.append(Path(destination).name)
            if Path(destination).name == "z.txt":
                raise PermissionError("persistent z replacement failure")
            return replace(source, destination)

        with patch.object(optional_transaction.os, "replace", side_effect=persistent_failure), \
                self.assertRaisesRegex(PermissionError, "persistent z"):
            commit(self.root, self.before, self.after)
        self.assertEqual(attempted, ["a.txt", "z.txt", "a.txt"])
        self.assert_original()

    def test_interrupt_immediately_after_replace_restores_the_inflight_path_too(self):
        replace = os.replace
        interrupted = False

        def interrupt_after_replace(source, destination):
            nonlocal interrupted
            replace(source, destination)
            if Path(destination).name == "z.txt" and not interrupted:
                interrupted = True
                raise KeyboardInterrupt()

        with patch.object(optional_transaction.os, "replace", side_effect=interrupt_after_replace), \
                self.assertRaises(KeyboardInterrupt):
            commit(self.root, self.before, self.after)
        self.assert_original()

    def test_persistent_temp_write_failure_never_truncates_existing_target(self):
        with patch.object(optional_transaction.os, "fchmod", side_effect=OSError("persistent chmod failure")), \
                self.assertRaisesRegex(OSError, "persistent chmod"):
            commit(self.root, self.before, self.after)
        self.assert_original()

    def test_persistent_rollback_failure_does_not_block_other_paths_or_directory_cleanup(self):
        before = {**self.before, "zz/new.txt": None}
        after = {**self.after, "zz/new.txt": (b"new", 0o644)}
        write = optional_transaction._write

        def persistent_failure(path, value):
            if path.name == "new.txt":
                raise OSError("persistent forward failure")
            if path.name == "z.txt" and value == self.before["z.txt"]:
                raise PermissionError("persistent z rollback failure")
            return write(path, value)

        with patch.object(optional_transaction, "_write", side_effect=persistent_failure), \
                self.assertRaises(ProjectionRollbackError) as error:
            commit(self.root, before, after)
        self.assertIn("persistent forward failure", str(error.exception))
        self.assertIn("z.txt: persistent z rollback failure", str(error.exception))
        self.assertIs(error.exception.__cause__, error.exception.original)
        self.assertEqual(state(self.root, "a.txt"), self.before["a.txt"])
        self.assertEqual(state(self.root, "z.txt"), self.after["z.txt"])
        self.assertFalse((self.root / "zz").exists())
        self.assertFalse(list(self.root.glob(".optional-write-*")))

    def test_cleanup_failure_is_reported_without_losing_original_or_skipping_restore(self):
        before = {**self.before, "nested/new.txt": None}
        after = {**self.after, "nested/new.txt": (b"new", 0o644)}
        write = optional_transaction._write
        rmdir = Path.rmdir

        def fail_write(path, value):
            if path.name == "new.txt":
                raise OSError("original write failure")
            return write(path, value)

        def fail_directory(path):
            if path == self.root / "nested":
                raise PermissionError("persistent directory cleanup failure")
            return rmdir(path)

        with patch.object(optional_transaction, "_write", side_effect=fail_write), \
                patch.object(Path, "rmdir", fail_directory), \
                self.assertRaises(ProjectionRollbackError) as error:
            commit(self.root, before, after)
        self.assertIn("original write failure", str(error.exception))
        self.assertIn("nested: persistent directory cleanup failure", str(error.exception))
        self.assertEqual(state(self.root, "a.txt"), self.before["a.txt"])
        self.assertEqual(state(self.root, "z.txt"), self.before["z.txt"])
        self.assertEqual(list((self.root / "nested").iterdir()), [])

    def test_temp_cleanup_failure_is_explicit_and_does_not_change_original(self):
        unlink = Path.unlink

        def fail_temporary(path, *args, **kwargs):
            if path.name.startswith(".optional-write-"):
                raise PermissionError("persistent temporary cleanup failure")
            return unlink(path, *args, **kwargs)

        with patch.object(optional_transaction.os, "replace", side_effect=OSError("replace failed")), \
                patch.object(Path, "unlink", fail_temporary), \
                self.assertRaises(ProjectionRollbackError) as error:
            commit(self.root, self.before, self.after)
        self.assertIn("replace failed", str(error.exception))
        self.assertIn("persistent temporary cleanup failure", str(error.exception))
        self.assertEqual(state(self.root, "a.txt"), self.before["a.txt"])
        self.assertEqual(len(list(self.root.glob(".optional-write-*"))), 1)


if __name__ == "__main__":
    unittest.main()
