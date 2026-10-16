"""Explicit, native lifecycle verification against privately extracted FPK bytes."""
import os
from pathlib import Path
import shutil
import tarfile

from .archive import inspect_fpk, safe_relative
from .directories import ensure_directory


class LifecycleFixture:
    """Model file availability, leaving business assertions and execution to the caller."""

    def __init__(self, package, root):
        self.package = Path(package).absolute()
        self.root = Path(root).absolute()
        if self.root.is_symlink() or not self.root.is_dir() or any(self.root.iterdir()):
            raise ValueError('lifecycle fixture requires an empty private directory')
        if self.root.resolve() != self.root or self.root.stat().st_uid != os.geteuid():
            raise ValueError('lifecycle fixture must have canonical current-user ownership')
        if self.root.stat().st_mode & 0o077:
            raise ValueError('lifecycle fixture directory must be private')
        inspect_fpk(self.package)
        self.events = []
        self.app = self.root / 'app'
        self.metadata = self.root / 'package'
        self.metadata.mkdir(mode=0o700)
        for name in ('etc', 'var', 'tmp'):
            (self.root / name).mkdir(mode=0o700)
        with tarfile.open(self.package, 'r:*') as archive:
            self._extract_members(archive, self.metadata, skip={'app.tgz'})
        self.environment = {
            'TRIM_APPDEST': str(self.app), 'TRIM_PKGETC': str(self.root / 'etc'),
            'TRIM_PKGVAR': str(self.root / 'var'), 'TRIM_PKGTMP': str(self.root / 'tmp'),
            'TRIM_TEMP_LOGFILE': str(self.root / 'callback-error.log'),
        }

    @staticmethod
    def _extract(archive, item, root):
        path = root / safe_relative(item.name)
        if item.isdir():
            ensure_directory(root, path)
            return
        if not item.isfile() or path.exists() or path.is_symlink():
            raise ValueError('fixture archive requires unique regular files')
        ensure_directory(root, path.parent)
        with archive.extractfile(item) as stream, path.open('xb') as output:
            shutil.copyfileobj(stream, output)
        path.chmod(item.mode & 0o777)

    @classmethod
    def _extract_members(cls, archive, root, *, skip=()):
        """Apply archive directory permissions after all children are extracted."""
        directories = []
        for item in archive:
            name = safe_relative(item.name)
            if name in skip:
                continue
            cls._extract(archive, item, root)
            if item.isdir():
                directories.append((root / name, item.mode & 0o777))
        for path, mode in sorted(directories, key=lambda item: len(item[0].parts),
                                 reverse=True):
            path.chmod(mode)

    def deploy(self):
        if self.app.exists() or self.app.is_symlink():
            raise ValueError('fixture target must be absent before deployment')
        self.app.mkdir(mode=0o700)
        with tarfile.open(self.package, 'r:*') as outer:
            item = next(item for item in outer if safe_relative(item.name) == 'app.tgz')
            with outer.extractfile(item) as stream, tarfile.open(fileobj=stream, mode='r:*') as inner:
                self._extract_members(inner, self.app)
        self.events.append({'event': 'deploy', 'target_present': True})

    def remove_target(self):
        if self.app.is_symlink() or self.app.parent != self.root:
            raise ValueError('unsafe fixture target')
        if self.app.exists():
            shutil.rmtree(self.app)
        self.events.append({'event': 'remove-target', 'target_present': False})

    def invoke(self, phase, run, environment=None):
        commands = {'main:start', 'main:stop', 'main:status'}
        if phase in commands:
            argv = [str(self.metadata / 'cmd/main'), phase.split(':')[1]]
        elif phase in ('install_init', 'install_callback', 'upgrade_init', 'upgrade_callback',
                       'uninstall_init', 'uninstall_callback', 'config_init', 'config_callback'):
            argv = [str(self.metadata / 'cmd' / phase)]
        else:
            raise ValueError('unknown lifecycle fixture phase')
        if set(environment or {}) & set(self.environment):
            raise ValueError('cannot override lifecycle fixture paths')
        self.events.append({'event': phase, 'target_present': self.app.is_dir()})
        return run(argv, self.root, {**self.environment, **(environment or {})})

    def install(self, run, environment=None):
        if self.app.exists():
            raise ValueError('install_init must be verified without a deployed target')
        self.invoke('install_init', run, environment)
        self.deploy()
        self.invoke('install_callback', run, environment)

    def upgrade(self, run, environment=None):
        if not self.app.is_dir():
            raise ValueError('upgrade requires the old target')
        self.invoke('upgrade_init', run, environment)
        self.remove_target()
        self.deploy()
        self.invoke('upgrade_callback', run, environment)

    def uninstall(self, run, environment=None):
        if not self.app.is_dir():
            raise ValueError('uninstall_init requires the installed target')
        self.invoke('uninstall_init', run, environment)
        self.remove_target()
        self.invoke('uninstall_callback', run, environment)
