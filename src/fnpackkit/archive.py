"""Read bounded regular FPK archives without extracting attacker-controlled paths."""
import hashlib
import tarfile
from pathlib import PurePosixPath


MAX_ENTRIES = 100000
MAX_FILE = 1024 ** 3
MAX_TOTAL = 2 * 1024 ** 3


def safe_relative(value):
    path = PurePosixPath(value)
    if (not isinstance(value, str) or not value or path.is_absolute()
            or ".." in path.parts or "\\" in value or "\x00" in value
            or not path.parts):
        raise ValueError(f"unsafe relative path: {value!r}")
    return path.as_posix()


def inventory(archive):
    """Hash regular files and reject duplicate, special, linked or excessive members."""
    entries, total = {}, 0
    for count, member in enumerate(archive, 1):
        name = safe_relative(member.name)
        if name in entries or count > MAX_ENTRIES:
            raise ValueError(f"duplicate entry or excessive count: {name}")
        if not (member.isfile() or member.isdir()) or member.mode & 0o7000:
            raise ValueError(f"unsafe archive member: {name}")
        total += member.size
        if member.size < 0 or member.size > MAX_FILE or total > MAX_TOTAL:
            raise ValueError("archive size limit exceeded")
        if member.isdir():
            entries[name] = None
            continue
        digest = hashlib.sha256()
        stream = archive.extractfile(member)
        if stream is None:
            raise ValueError(f"unreadable archive entry: {name}")
        size = 0
        with stream:
            while chunk := stream.read(1024 * 1024):
                size += len(chunk)
                digest.update(chunk)
        if size != member.size:
            raise ValueError(f"truncated archive entry: {name}")
        entries[name] = {"sha256": digest.hexdigest(), "mode": member.mode & 0o777,
                         "size": size}
    # A regular file cannot also serve as another member's parent.
    for name in entries:
        for parent in PurePosixPath(name).parents:
            if parent.as_posix() in entries and entries[parent.as_posix()] is not None:
                raise ValueError(f"file used as archive directory: {parent}")
    return {name: value for name, value in entries.items() if value is not None}


def inspect_fpk(path):
    """Return separate outer metadata and inner application inventories."""
    with tarfile.open(path, "r:*") as outer:
        package = inventory(outer)
        if "app.tgz" not in package or "manifest" not in package:
            raise ValueError("FPK requires manifest and app.tgz")
        member = next(item for item in outer.getmembers()
                      if safe_relative(item.name) == "app.tgz")
        stream = outer.extractfile(member)
        with stream, tarfile.open(fileobj=stream, mode="r:*") as inner:
            application = inventory(inner)
    return package, application
