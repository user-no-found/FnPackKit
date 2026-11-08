"""Traversal budget, policy loading and entry checks for the uninstall helper."""
import json
import os
from pathlib import Path
import re
import stat

MAX_ENTRIES = 1_000_000
MAX_DEPTH = 256
FLAGS = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC


class Rejected(Exception):
    """Diagnostics use stable reasons and never include paths or file contents."""


class Budget:
    """Bound one standalone traversal without depending on recursion limits."""

    def __init__(self):
        self.entries = 0

    def counted(self, depth):
        self.entries += 1
        if depth > MAX_DEPTH or self.entries > MAX_ENTRIES:
            raise Rejected('tree_budget_exceeded')


def policy_data(policy=None):
    if policy is not None:
        return policy
    path = Path(__file__).with_name('fnpack_uninstall_policy.json')
    try:
        data = json.loads(path.read_text(encoding='utf-8'))
    except FileNotFoundError:
        raise Rejected('policy_unavailable') from None
    except (OSError, ValueError) as error:
        raise Rejected('policy_invalid') from error
    if not isinstance(data, dict) or not isinstance(data.get('app_id'), str):
        raise Rejected('policy_invalid')
    return data


def trusted(info):
    if info.st_uid != 0 or info.st_mode & 0o022:
        raise Rejected('untrusted_namespace')


def open_absolute(path):
    if not path.startswith('/') or os.path.normpath(path) != path or path == '/':
        raise Rejected('invalid_volume')
    fd = os.open('/', FLAGS)
    try:
        for name in path.split('/')[1:]:
            parent = os.fstat(fd)
            if parent.st_uid != 0 or (parent.st_mode & 0o022 and
                                     not parent.st_mode & stat.S_ISVTX):
                raise Rejected('untrusted_ancestor')
            child = os.open(name, FLAGS, dir_fd=fd)
            os.close(fd)
            fd = child
        trusted(os.fstat(fd))
        return fd
    except BaseException:
        os.close(fd)
        raise


def mountpoints():
    result = set()
    with open('/proc/self/mountinfo', encoding='utf-8') as stream:
        for line in stream:
            value = line.split()[4]
            value = re.sub(r'\\([0-7]{3})', lambda m: chr(int(m[1], 8)), value)
            result.add(value)
    return result


def check_entry(info, device, uid):
    if info.st_dev != device:
        raise Rejected('cross_device_entry')
    if info.st_uid not in (0, uid):
        raise Rejected('unexpected_owner')
    if stat.S_ISREG(info.st_mode) and info.st_nlink != 1:
        raise Rejected('hardlinked_entry')
    if stat.S_ISCHR(info.st_mode) or stat.S_ISBLK(info.st_mode):
        raise Rejected('device_entry')


def same_entry(parent, name, previous):
    current = os.stat(name, dir_fd=parent, follow_symlinks=False)
    if (current.st_dev, current.st_ino) != (previous.st_dev, previous.st_ino):
        raise Rejected('entry_changed')


def nas_owner(parent, name, default):
    info = os.stat(name, dir_fd=parent, follow_symlinks=False)
    if stat.S_ISDIR(info.st_mode):
        if (not name.isdecimal() or str(int(name)) != name or
                not 1000 <= int(name) < 4294967295 or int(name) == 65534 or
                info.st_uid not in (0, int(name)) or info.st_mode & 0o077):
            raise Rejected('invalid_nas_user_tree')
        return int(name)
    if info.st_uid != 0:
        raise Rejected('unexpected_control_owner')
    return default
