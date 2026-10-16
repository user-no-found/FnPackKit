"""Private, short-lived directories for executing native verification fixtures."""
import os
from pathlib import Path
import tempfile


def execution_directory():
    """Use an executable /tmp mount independently of data-only TMPDIR settings."""
    parent = Path('/tmp').resolve(strict=True)
    if os.statvfs(parent).f_flag & os.ST_NOEXEC:
        raise ValueError(f'native verification requires an executable temporary mount: {parent} is noexec')
    return Path(tempfile.mkdtemp(prefix='fpk-', dir=parent))
