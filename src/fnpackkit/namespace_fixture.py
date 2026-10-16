"""Root-owned synthetic fnOS namespace for actual FPK lifecycle verification."""
import os
from pathlib import Path
import pwd
import re
import shutil
import tarfile

from .archive import inspect_fpk
from .directories import ensure_directory
from .lifecycle_verify import LifecycleFixture


class NamespaceFixture(LifecycleFixture):
    """Requires explicit root execution; application commands still drop privilege."""

    def __init__(self, package, root, username):
        self.package = Path(package).absolute()
        self.root = Path(root).absolute()
        if os.geteuid() != 0 or self.root.is_symlink() or not self.root.is_dir():
            raise ValueError('namespace fixture requires a root-owned private directory')
        if (self.root.resolve() != self.root or self.root.stat().st_uid != 0 or
                self.root.stat().st_mode & 0o077 or any(self.root.iterdir())):
            raise ValueError('namespace fixture root must be canonical, empty and private')
        account = pwd.getpwnam(username)
        if account.pw_uid == 0:
            raise ValueError('namespace fixture application account must be nonroot')
        self.account = account
        inspect_fpk(self.package)
        self.events = []
        self.metadata = self.root / 'package'
        ensure_directory(self.root, self.metadata)
        with tarfile.open(self.package, 'r:*') as archive:
            manifest = archive.extractfile('manifest').read().decode()
            self.app_id = next(line.split('=', 1)[1].strip() for line in manifest.splitlines()
                               if line.split('=', 1)[0].strip() == 'appname')
            if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9._-]*', self.app_id):
                raise ValueError('unsafe namespace fixture application identifier')
            self._extract_members(archive, self.metadata, skip={'app.tgz'})
        self.volume = self.root / 'volume'
        ensure_directory(self.root, self.volume)
        self.roots = {}
        self.environment = {'TRIM_APPNAME': self.app_id,
                            'TRIM_APPDEST_VOL': str(self.volume), 'TRIM_USERNAME': username,
                            'TRIM_UID': str(account.pw_uid), 'TRIM_GID': str(account.pw_gid),
                            'TRIM_RUN_UID': '0', 'TRIM_RUN_GID': '0',
                            'TRIM_TEMP_LOGFILE': str(self.root / 'callback-error.log')}
        names = {'etc': ('TRIM_PKGETC', '@appconf'), 'var': ('TRIM_PKGVAR', '@appdata'),
                 'home': ('TRIM_PKGHOME', '@apphome'), 'meta': ('TRIM_PKGMETA', '@appmeta'),
                 'tmp': ('TRIM_PKGTMP', '@apptemp')}
        for label, (variable, namespace) in names.items():
            ensure_directory(self.volume, self.volume / namespace)
            self.roots[label] = self.volume / namespace / self.app_id
            self.environment[variable] = str(self.roots[label])
        ensure_directory(self.volume, self.volume / '@appcenter')
        self.app = self.volume / '@appcenter' / self.app_id
        self.environment['TRIM_APPDEST'] = str(self.app)
        self.root.chmod(0o711)

    def create_roots(self):
        for path in self.roots.values():
            if not path.exists():
                path.mkdir(mode=0o700)
                # Root lifecycle mode creates roots owned by root; shared prepare hands them back.

    def deploy(self):
        super().deploy()
        self.app.chmod(0o755)

    def remove_target(self):
        if self.app.is_symlink() or self.app.parent != self.volume / '@appcenter':
            raise ValueError('unsafe namespace fixture target')
        if self.app.exists():
            shutil.rmtree(self.app)
        self.events.append({'event': 'remove-target', 'target_present': False})

    def install(self, run, environment=None):
        if self.app.exists():
            raise ValueError('namespace fixture installation requires absent runtime')
        self.invoke('install_init', run, environment)
        self.deploy()
        self.create_roots()
        self.invoke('install_callback', run, environment)

    def uninstall(self, run, environment=None):
        if not self.app.is_dir():
            raise ValueError('namespace fixture uninstall requires runtime')
        self.invoke('uninstall_init', run, environment)
        self.remove_target()
        if self.roots['tmp'].exists():
            shutil.rmtree(self.roots['tmp'])
        self.invoke('uninstall_callback', run, environment)

    def upgrade(self, run, environment=None):
        """Model root-mode ownership handoff before the old service stop hook."""
        for root in self.roots.values():
            for path in [root, *root.rglob('*')]:
                os.chown(path, 0, 0, follow_symlinks=False)
            root.chmod(0o770)
        super().upgrade(run, environment)
