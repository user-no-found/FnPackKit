"""Preflight and roll back regular-file changes in an exclusively owned private stage."""
import os
from pathlib import Path
import stat
import tempfile

from .optional_contracts import checked_path, read_regular


def state(root, name):
    path = checked_path(root, name, missing=True)
    if not path.exists():
        return None
    content = read_regular(root, name)
    mode = stat.S_IMODE(path.stat().st_mode)
    return content, mode


def _parents(path, root, created):
    pending = []
    current = path.parent
    while current != root and not current.exists():
        pending.append(current)
        current = current.parent
    for directory in reversed(pending):
        directory.mkdir()
        created.append(directory)


class ProjectionRollbackError(RuntimeError):
    """Keep the primary failure and every cleanup failure visible to build reporters."""

    def __init__(self, original, failures):
        self.original = original
        self.failures = tuple(failures)
        details = "; ".join(f"{path}: {error}" for path, error in failures)
        super().__init__(f"optional projection failed: {original}; rollback/cleanup incomplete: {details}")


def _write(path, value):
    """Replace only after a complete same-directory write, preserving a failed target."""
    content, mode = value
    descriptor, name = tempfile.mkstemp(prefix=".optional-write-", dir=path.parent)
    temporary = Path(name)
    try:
        with os.fdopen(descriptor, "wb") as stream:
            stream.write(content)
            os.fchmod(stream.fileno(), mode)
        os.replace(temporary, path)
    except BaseException as error:
        try:
            temporary.unlink(missing_ok=True)
        except BaseException as cleanup_error:
            raise ProjectionRollbackError(error, [(str(temporary), cleanup_error)]) from error
        raise


def commit(root, before, after):
    """All validation must already have passed; restore exact bytes/modes on failure."""
    root = Path(root)
    if any(state(root, name) != value for name, value in before.items()):
        raise ValueError("optional target changed during preflight")
    created, attempted = [], []
    try:
        for name, value in after.items():
            if value == before[name]:
                continue
            path = checked_path(root, name, missing=True)
            attempted.append(name)
            if value is None:
                path.unlink()
            else:
                _parents(path, root, created)
                _write(path, value)
    except BaseException as error:
        failures = []
        for name in reversed(attempted):
            try:
                path = checked_path(root, name, missing=True)
                original = before[name]
                # A failed atomic write needs no restore; an interrupt after replace does.
                if state(root, name) == original:
                    continue
                if original is None:
                    path.unlink(missing_ok=True)
                else:
                    _write(path, original)
            except BaseException as rollback_error:
                failures.append((name, rollback_error))
        for directory in reversed(created):
            try:
                directory.rmdir()
            except BaseException as cleanup_error:
                failures.append((str(directory.relative_to(root)), cleanup_error))
        if failures:
            raise ProjectionRollbackError(error, failures) from error
        raise
