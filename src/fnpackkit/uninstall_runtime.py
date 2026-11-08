"""Standalone fd-anchored cleanup; survives removal of the application payload."""
import contextlib
import grp
import os
import pwd
import stat
import sys

try:
    from .fnpack_uninstall_support import (Budget, FLAGS, Rejected, check_entry, mountpoints,
                                           nas_owner, open_absolute, policy_data, same_entry, trusted)
except ImportError:
    sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
    from fnpack_uninstall_support import (Budget, FLAGS, Rejected, check_entry, mountpoints,
                                          nas_owner, open_absolute, policy_data, same_entry, trusted)

POLICY = None
DIRECTORIES = {'etc': ('TRIM_PKGETC', '@appconf'), 'var': ('TRIM_PKGVAR', '@appdata'),
               'home': ('TRIM_PKGHOME', '@apphome'), 'meta': ('TRIM_PKGMETA', '@appmeta'),
               'tmp': ('TRIM_PKGTMP', '@apptemp')}


def scan(parent, name, device, uid, nas=False, budget=None, depth=1):
    """Reject every unsafe tree before the first removal; never follows links."""
    budget = budget or Budget()
    budget.counted(depth)
    info = os.stat(name, dir_fd=parent, follow_symlinks=False)
    check_entry(info, device, uid)
    if stat.S_ISDIR(info.st_mode):
        fd = os.open(name, FLAGS, dir_fd=parent)
        try:
            same_entry(parent, name, os.fstat(fd))
            with os.scandir(fd) as entries:
                for entry in entries:
                    scan(fd, entry.name, device, nas_owner(fd, entry.name, uid) if nas else uid,
                         budget=budget, depth=depth + 1)
        finally:
            os.close(fd)


def remove(parent, name, device, uid, nas=False, budget=None, depth=1):
    budget = budget or Budget()
    budget.counted(depth)
    info = os.stat(name, dir_fd=parent, follow_symlinks=False)
    check_entry(info, device, uid)
    if stat.S_ISDIR(info.st_mode):
        fd = os.open(name, FLAGS, dir_fd=parent)
        try:
            same_entry(parent, name, os.fstat(fd))
            with os.scandir(fd) as entries:
                for entry in entries:
                    remove(fd, entry.name, device, nas_owner(fd, entry.name, uid) if nas else uid,
                           budget=budget, depth=depth + 1)
            same_entry(parent, name, info)
            os.rmdir(name, dir_fd=parent)
        finally:
            os.close(fd)
    else:
        same_entry(parent, name, info)
        os.unlink(name, dir_fd=parent)


def descendants(fd, parts, device, uid, budget=None, depth=1):
    """Yield open parent descriptors for bounded, declared transient patterns."""
    budget = budget or Budget()
    entries = os.scandir(fd) if parts[0] == '*' else None
    names = (entry.name for entry in entries) if entries is not None else [parts[0]]
    try:
        for name in names:
            budget.counted(depth)
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
                    yield from descendants(child, parts[1:], device, uid, budget=budget, depth=depth + 1)
                finally:
                    os.close(child)
    finally:
        if entries is not None:
            entries.close()


class Cleanup:
    def __init__(self, stack):
        if os.geteuid() != 0:
            raise Rejected('root_lifecycle_required')
        policy = policy_data(POLICY)
        self.policy = policy
        self.app = policy['app_id']
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
        nas_roots = self.policy.get('nas_user_roots', ())
        if self.action == 'delete':
            for label, (parent, fd) in self.roots.items():
                yield parent, self.app, label in nas_roots
            return
        if 'tmp' in self.roots:
            yield self.roots['tmp'][0], self.app, 'tmp' in nas_roots
        for value in self.policy['transient']:
            label, *parts = value.split('/')
            if label in self.roots:
                for parent, name in descendants(self.roots[label][1], parts, self.device, self.uid,
                                                budget=Budget()):
                    yield parent, name, False

    def preflight(self):
        budget = Budget()
        for parent, name, nas in self.targets():
            scan(parent, name, self.device, self.uid, nas, budget=budget)

    def prepare_nas(self):
        # UID children remain private NAS-owned trees; authority stays root-owned.
        nas_roots = self.policy['nas_user_roots']
        budget = Budget()
        for label, (parent, fd) in self.roots.items():
            if label in nas_roots:
                with os.scandir(fd) as entries:
                    for entry in entries:
                        nas_owner(fd, entry.name, self.uid)
            else:
                scan(parent, self.app, self.device, self.uid, budget=budget)
        for label, (parent, fd) in self.roots.items():
            same_entry(parent, self.app, os.fstat(fd))
            os.fchown(fd, 0, 0)
            os.fchmod(fd, 0o711 if label in nas_roots else 0o700)

    def prepare(self):
        """Platform root mode creates root-owned state; hand private state to the app."""
        if self.policy.get('state_owner') == 'root':
            self.prepare_root()
            return
        if self.policy.get('nas_user_roots'):
            self.prepare_nas()
            return
        selected = [(parent, fd) for parent, fd in self.roots.values()
                    if os.fstat(fd).st_uid == 0]
        budget = Budget()
        for parent, fd in selected:
            scan(parent, self.app, self.device, self.uid, budget=budget)
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
        def ownership(parent, name, budget=None, depth=1):
            budget = budget or Budget()
            budget.counted(depth)
            info = os.stat(name, dir_fd=parent, follow_symlinks=False)
            check_entry(info, self.device, self.uid)
            if stat.S_ISDIR(info.st_mode):
                fd = os.open(name, FLAGS, dir_fd=parent)
                try:
                    same_entry(parent, name, os.fstat(fd))
                    with os.scandir(fd) as entries:
                        for entry in entries:
                            ownership(fd, entry.name, budget=budget, depth=depth + 1)
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
            ownership(parent, self.app, budget=Budget())
        for parent, fd in self.roots.values():
            same_entry(parent, self.app, os.fstat(fd))
            os.fchown(fd, self.uid, self.gid)
            os.fchmod(fd, 0o700)

    def prepare_root(self):
        """Keep explicitly privileged bridge state and its executable root-owned."""
        entries = [(parent, self.app, True) for parent, _ in self.roots.values()]
        parent = os.open('@appcenter', FLAGS, dir_fd=self.volume_fd)
        try:
            trusted(os.fstat(parent))
            try:
                os.stat(self.app, dir_fd=parent, follow_symlinks=False)
            except FileNotFoundError:
                pass
            else:
                runtime = os.environ['TRIM_APPDEST']
                if any(m == runtime or m.startswith(runtime + '/') for m in self.mounts):
                    raise Rejected('mounted_runtime_entry')
                entries.append((parent, self.app, False))
            budget = Budget()
            for directory, name, _ in entries:
                scan(directory, name, self.device, self.uid, budget=budget)

            def ownership(directory, name, private, budget=None, depth=1):
                budget = budget or Budget()
                budget.counted(depth)
                info = os.stat(name, dir_fd=directory, follow_symlinks=False)
                check_entry(info, self.device, self.uid)
                if stat.S_ISLNK(info.st_mode):
                    return
                # Runtime IPC has an application-owned access policy. It is not
                # immutable payload and must keep its active socket/FIFO group.
                if not private and (stat.S_ISSOCK(info.st_mode) or stat.S_ISFIFO(info.st_mode)):
                    return
                fd = os.open(name, FLAGS if stat.S_ISDIR(info.st_mode) else
                             os.O_PATH | os.O_NOFOLLOW | os.O_CLOEXEC, dir_fd=directory)
                try:
                    current = os.fstat(fd)
                    check_entry(current, self.device, self.uid)
                    if (current.st_ino != info.st_ino or current.st_dev != info.st_dev or
                            stat.S_ISLNK(current.st_mode)):
                        raise Rejected('entry_changed')
                    same_entry(directory, name, current)
                    if stat.S_ISDIR(info.st_mode):
                        with os.scandir(fd) as entries:
                            for entry in entries:
                                ownership(fd, entry.name, private, budget=budget, depth=depth + 1)
                        os.fchmod(fd, 0o700 if private else 0o755)
                    elif stat.S_ISREG(current.st_mode):
                        # Installer defaults may add group/other access. Keep
                        # private state private and runtime files non-writable.
                        mode = (current.st_mode & 0o700) | 0o600 if private else current.st_mode & 0o755
                        os.chmod('/proc/self/fd/' + str(fd), mode)
                    os.chown('/proc/self/fd/' + str(fd), 0, 0)
                finally:
                    os.close(fd)

            for directory, name, private in entries:
                ownership(directory, name, private, budget=Budget())
        finally:
            os.close(parent)

    def cleanup(self):
        self.preflight()
        budget = Budget()
        for parent, name, nas in self.targets():
            remove(parent, name, self.device, self.uid, nas, budget=budget)
        if self.action == 'retain':
            for label in ('home', 'meta'):
                if label in self.roots:
                    parent, fd = self.roots[label]
                    with os.scandir(fd) as entries:
                        empty = next(entries, None) is None
                    if empty:
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
