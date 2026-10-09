"""Read the local fnOS application version without contacting privileged services."""
from pathlib import Path
import re
import stat

from buildflow.contracts import BuildError
from .formats import manifest_fields


def installed_version(project, app_root=Path("/var/apps")):
    """Return the installed manifest's version, or None when the app directory is absent.

    fnOS owns the installation root. Permission, format and identity failures
    remain errors instead of being reported as an uninstalled application.
    """
    app_id = project.data["app_id"]
    if not isinstance(app_id, str) or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]*", app_id):
        raise BuildError("cannot inspect installed version: unsafe app_id")
    application = Path(app_root) / app_id
    manifest = application / "manifest"
    try:
        try:
            application_info = application.stat()
        except FileNotFoundError:
            if application.is_symlink():
                raise ValueError("application directory is a broken link")
            return None
        if not stat.S_ISDIR(application_info.st_mode):
            raise ValueError("application installation is not a directory")
        info = manifest.stat()
        if not stat.S_ISREG(info.st_mode) or info.st_size > 65536:
            raise ValueError("manifest must be a regular file of at most 64 KiB")
        with manifest.open("r", encoding="utf-8") as stream:
            text = stream.read(65537)
        if len(text.encode("utf-8")) > 65536:
            raise ValueError("manifest exceeds 64 KiB")
        fields = manifest_fields(text)
        if fields.get("appname") != app_id:
            raise ValueError("manifest appname does not match requested application")
        version = fields.get("version", "")
        if not re.fullmatch(r"[0-9][A-Za-z0-9._+-]*", version):
            raise ValueError("manifest has no valid version field")
        return version
    except (OSError, ValueError) as exc:
        raise BuildError(f"cannot inspect installed version at {manifest}: {exc}") from exc
