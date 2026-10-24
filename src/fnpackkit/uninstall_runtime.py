"""Standalone fd-anchored cleanup; survives removal of the application payload."""
import contextlib
import grp
import os
import pwd
import re
import stat
import sys

POLICY = None
DIRECTORIES = {'etc': ('TRIM_PKGETC', '@appconf'), 'var': ('TRIM_PKGVAR', '@appdata'),
               'home': ('TRIM_PKGHOME', '@apphome'), 'meta': ('TRIM_PKGMETA', '@appmeta'),
               'tmp': ('TRIM_PKGTMP', '@apptemp')}
FLAGS = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC


class Rejected(Exception):
    """Diagnostics use stable reasons and never include paths or file contents."""


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


def scan(parent, name, device, uid, nas=False):
    """Reject every unsafe tree before the first removal; never follows links."""
    info = os.stat(name, dir_fd=parent, follow_symlinks=False)
    check_entry(info, device, uid)
    if stat.S_ISDIR(info.st_mode):
        fd = os.open(name, FLAGS, dir_fd=parent)
        try:
            same_entry(parent, name, os.fstat(fd))
            for child in os.listdir(fd):
                scan(fd, child, device, nas_owner(fd, child, uid) if nas else uid)
        finally:
            os.close(fd)


def remove(parent, name, device, uid, nas=False):
    info = os.stat(name, dir_fd=parent, follow_symlinks=False)
    check_entry(info, device, uid)
    if stat.S_ISDIR(info.st_mode):
        fd = os.open(name, FLAGS, dir_fd=parent)
        try:
            same_entry(parent, name, os.fstat(fd))
            for child in os.listdir(fd):
                remove(fd, child, device, nas_owner(fd, child, uid) if nas else uid)
            same_entry(parent, name, info)
            os.rmdir(name, dir_fd=parent)
        finally:
            os.close(fd)
    else:
        same_entry(parent, name, info)
        os.unlink(name, dir_fd=parent)


def descendants(fd, parts, device, uid):
    """Yield open parent descriptors for bounded, declared transient patterns."""
    names = os.listdir(fd) if parts[0] == '*' else [parts[0]]
    for name in names:
        try:
            info = os.stat(name, dir_fd=fd, follow_symlinks=False)
        except FileNotFoundError:
            continue
        check_entry(info, device, uid)
        if len(parts) == 1:
            yield fd, name
        else:
            if not stat.S_ISDIR(info.st_mode):
                raise Rejected('unsafe_transient_parent')
            child = os.open(name, FLAGS, dir_fd=fd)
            try:
                same_entry(fd, name, os.fstat(child))
                yield from descendants(child, parts[1:], device, uid)
            finally:
                os.close(child)


class Cleanup:
    def __init__(self, stack):
        if os.geteuid() != 0 or POLICY is None:
            raise Rejected('root_lifecycle_required')
        self.app = POLICY['app_id']
        if os.environ.get('TRIM_APPNAME') != self.app:
            raise Rejected('application_identity_mismatch')
        uid = os.environ.get('TRIM_UID', '')
        gid = os.environ.get('TRIM_GID', '')
        if not uid.isdecimal() or not gid.isdecimal() or int(uid) == 0 or int(gid) == 0:
            raise Rejected('invalid_package_identity')
        self.uid = int(uid)
        self.gid = int(gid)
        account = pwd.getpwnam(os.environ.get('TRIM_USERNAME', ''))
        group = grp.getgrgid(int(gid))
        if account.pw_uid != self.uid or (account.pw_gid != group.gr_gid and account.pw_name not in group.gr_mem):
            raise Rejected('package_identity_mismatch')
        self.action = os.environ.get('wizard_data_action', 'retain')
        if self.action not in ('retain', 'delete'):
            raise Rejected('invalid_data_action')
        volume = os.environ.get('TRIM_APPDEST_VOL', '')
        volume_fd = open_absolute(volume)
        self.volume_fd = volume_fd
        stack.callback(os.close, volume_fd)
        self.device = os.fstat(volume_fd).st_dev
        if os.environ.get('TRIM_APPDEST') != volume + '/@appcenter/' + self.app:
            raise Rejected('target_namespace_mismatch')
        self.roots = {}
        mounts = mountpoints()
        self.mounts = mounts
        for label, (variable, namespace) in DIRECTORIES.items():
            path = volume + '/' + namespace + '/' + self.app
            if os.environ.get(variable) != path:
                raise Rejected('data_namespace_mismatch')
            if any(m == path or m.startswith(path + '/') for m in mounts):
                raise Rejected('mounted_data_entry')
            parent = os.open(namespace, FLAGS, dir_fd=volume_fd)
            stack.callback(os.close, parent)
            trusted(os.fstat(parent))
            try:
                info = os.stat(self.app, dir_fd=parent, follow_symlinks=False)
            except FileNotFoundError:
                continue
            if not stat.S_ISDIR(info.st_mode) or info.st_uid not in (0, self.uid):
                raise Rejected('unsafe_data_root')
            check_entry(info, self.device, self.uid)
            fd = os.open(self.app, FLAGS, dir_fd=parent)
            stack.callback(os.close, fd)
            same_entry(parent, self.app, os.fstat(fd))
            self.roots[label] = (parent, fd)

    def targets(self):
        if self.action == 'delete':
            for label, (parent, fd) in self.roots.items():
                yield parent, self.app, label in POLICY.get('nas_user_roots', ())
            return
        if 'tmp' in self.roots:
            yield self.roots['tmp'][0], self.app, 'tmp' in POLICY.get('nas_user_roots', ())
        for value in POLICY['transient']:
            label, *parts = value.split('/')
            if label in self.roots:
                for parent, name in descendants(self.roots[label][1], parts, self.device, self.uid):
                    yield parent, name, False

    def preflight(self):
        for parent, name, nas in self.targets():
            scan(parent, name, self.device, self.uid, nas)

    def prepare_nas(self):
        # UID children remain private NAS-owned trees; authority stays root-owned.
        for label, (parent, fd) in self.roots.items():
            if label in POLICY['nas_user_roots']:
                for name in os.listdir(fd):
                    nas_owner(fd, name, self.uid)
            else:
                scan(parent, self.app, self.device, self.uid)
        for label, (parent, fd) in self.roots.items():
            same_entry(parent, self.app, os.fstat(fd))
            os.fchown(fd, 0, 0)
            os.fchmod(fd, 0o711 if label in POLICY['nas_user_roots'] else 0o700)

    def prepare(self):
        """Platform root mode creates root-owned state; hand private state to the app."""
        if POLICY.get('nas_user_roots'):
            self.prepare_nas()
            return
        selected = [(parent, fd) for parent, fd in self.roots.values()
                    if os.fstat(fd).st_uid == 0]
        for parent, fd in selected:
            scan(parent, self.app, self.device, self.uid)
        parent = os.open('@appcenter', FLAGS, dir_fd=self.volume_fd)
        try:
            trusted(os.fstat(parent))
            try:
                info = os.stat(self.app, dir_fd=parent, follow_symlinks=False)
            except FileNotFoundError:
                info = None
            if info is not None:
                runtime = os.environ['TRIM_APPDEST']
                if any(m == runtime or m.startswith(runtime + '/') for m in self.mounts):
                    raise Rejected('mounted_runtime_entry')
                if not stat.S_ISDIR(info.st_mode) or info.st_uid not in (0, self.uid):
                    raise Rejected('unsafe_runtime_root')
                check_entry(info, self.device, self.uid)
                fd = os.open(self.app, FLAGS, dir_fd=parent)
                try:
                    same_entry(parent, self.app, os.fstat(fd))
                    # The gateway socket lives in the runtime root; payload descendants remain unchanged.
                    os.fchown(fd, self.uid, self.gid)
                    os.fchmod(fd, 0o755)
                finally:
                    os.close(fd)
        finally:
            os.close(parent)
        def ownership(parent, name):
            info = os.stat(name, dir_fd=parent, follow_symlinks=False)
            check_entry(info, self.device, self.uid)
            if stat.S_ISDIR(info.st_mode):
                fd = os.open(name, FLAGS, dir_fd=parent)
                try:
                    same_entry(parent, name, os.fstat(fd))
                    for child in os.listdir(fd):
                        ownership(fd, child)
                    os.fchown(fd, self.uid, self.gid)
                    os.fchmod(fd, 0o700)
                finally:
                    os.close(fd)
            elif not stat.S_ISLNK(info.st_mode):
                # O_PATH anchors regular files, sockets and FIFOs without reading or blocking.
                fd = os.open(name, os.O_PATH | os.O_NOFOLLOW | os.O_CLOEXEC, dir_fd=parent)
                try:
                    current = os.fstat(fd)
                    check_entry(current, self.device, self.uid)
                    if (current.st_ino != info.st_ino or current.st_dev != info.st_dev or
                            stat.S_ISLNK(current.st_mode)):
                        raise Rejected('entry_changed')
                    same_entry(parent, name, current)
                    # The kernel fd link names this validated non-symlink inode, even if renamed.
                    os.chown('/proc/self/fd/' + str(fd), self.uid, self.gid)
                finally:
                    os.close(fd)
        for parent, fd in selected:
            ownership(parent, self.app)
        for parent, fd in self.roots.values():
            same_entry(parent, self.app, os.fstat(fd))
            os.fchown(fd, self.uid, self.gid)
            os.fchmod(fd, 0o700)

    def cleanup(self):
        self.preflight()
        for parent, name, nas in self.targets():
            remove(parent, name, self.device, self.uid, nas)
        if self.action == 'retain':
            for label in ('home', 'meta'):
                if label in self.roots:
                    parent, fd = self.roots[label]
                    if not os.listdir(fd):
                        same_entry(parent, self.app, os.fstat(fd))
                        os.rmdir(self.app, dir_fd=parent)


def main():
    try:
        with contextlib.ExitStack() as stack:
            cleanup = Cleanup(stack)
            if sys.argv[1:] == ['preflight']:
                cleanup.preflight()
                cleanup.prepare()
            elif sys.argv[1:] == ['cleanup']:
                cleanup.cleanup()
            elif sys.argv[1:] == ['prepare']:
                cleanup.prepare()
            else:
                raise Rejected('unknown_cleanup_phase')
    except Rejected as error:
        print('shared uninstall: ' + str(error), file=sys.stderr)
        return 1
    except (OSError, KeyError, ValueError, RecursionError):
        print('shared uninstall: filesystem_validation_failed', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
