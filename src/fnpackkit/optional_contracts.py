"""Bounded filesystem and local Git identity contracts for optional inputs."""
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import stat
import subprocess
from urllib.parse import urlsplit

MAX_FILE = 16 * 1024 * 1024
MAX_TOTAL = 128 * 1024 * 1024
ID = re.compile(r"[a-z0-9]+(?:-[a-z0-9]+)*\Z")
GIT_ENV = {"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": os.devnull,
           "GIT_TERMINAL_PROMPT": "0", "GIT_OPTIONAL_LOCKS": "0", "GIT_CONFIG_COUNT": "0"}


def sha256(content):
    return hashlib.sha256(content).hexdigest()


def json_bytes(value):
    return (json.dumps(value, ensure_ascii=False, sort_keys=True, indent=2) + "\n").encode()


def relative(value):
    if not isinstance(value, str) or not value or any(ord(c) < 32 for c in value):
        raise ValueError(f"unsafe optional path: {value!r}")
    parts = PurePosixPath(value).parts
    if (value.startswith("/") or "\\" in value or ":" in value or not parts
            or any(p in {"", ".", ".."} or p.casefold() == ".git" for p in value.split("/"))):
        raise ValueError(f"unsafe optional path: {value!r}")
    return value


def checked_directory(root):
    root = Path(root).absolute()
    current = Path(root.anchor)
    for part in root.parts[1:]:
        current /= part
        info = current.lstat()
        if stat.S_ISLNK(info.st_mode) or not stat.S_ISDIR(info.st_mode):
            raise ValueError(f"optional directory ancestor must not be a symlink/non-directory: {current}")
    return root


def checked_path(root, name, *, missing=False):
    """Reject links in every ancestor, including the supplied root."""
    root = checked_directory(root)
    current = root
    for part in PurePosixPath(relative(name)).parts:
        current /= part
        try:
            info = current.lstat()
        except FileNotFoundError:
            if missing:
                continue
            raise ValueError(f"missing optional input: {current}") from None
        if stat.S_ISLNK(info.st_mode):
            raise ValueError(f"symlink is not an optional input/target: {current}")
        if current != root / name and not stat.S_ISDIR(info.st_mode):
            raise ValueError(f"optional path ancestor is not a directory: {current}")
    return current


def read_regular(root, name, *, limit=MAX_FILE):
    path = checked_path(root, name)
    info = path.lstat()
    if (not stat.S_ISREG(info.st_mode) or info.st_nlink != 1
            or info.st_mode & 0o7000 or info.st_size > limit):
        raise ValueError(f"optional input must be a bounded unlinked regular file: {path}")
    with path.open("rb") as stream:
        content = stream.read(limit + 1)
    if len(content) > limit:
        raise ValueError(f"optional input exceeds size limit: {path}")
    current = path.lstat()
    fields = ("st_dev", "st_ino", "st_mode", "st_nlink", "st_size", "st_mtime_ns", "st_ctime_ns")
    if any(getattr(current, key) != getattr(info, key) for key in fields):
        raise ValueError(f"optional input changed while reading: {path}")
    return content


def git(root, *args, runner=None, confidential=False):
    argv = ["git", "-c", "core.hooksPath=" + os.devnull, "-c", "core.fsmonitor=false", *args]
    root = Path(root).absolute()
    environment = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}
    overrides = {**GIT_ENV, "GIT_DIR": str(root / ".git"), "GIT_WORK_TREE": str(root),
                 "GIT_COMMON_DIR": str(root / ".git"), "GIT_INDEX_FILE": str(root / ".git/index"),
                 "GIT_OBJECT_DIRECTORY": str(root / ".git/objects"),
                 "GIT_ALTERNATE_OBJECT_DIRECTORIES": "", "GIT_CONFIG": str(root / ".git/config"),
                 "GIT_NAMESPACE": "", "GIT_NO_REPLACE_OBJECTS": "1"}
    if runner is not None and not confidential:
        return runner.capture(argv, cwd=root, env=overrides).strip()
    result = subprocess.run(argv, cwd=root, env={**environment, **overrides},
                            text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if result.returncode:
        if confidential:
            raise ValueError("cannot read optional Git origin without exposing configuration")
        raise ValueError(f"local Git optional validation failed: {result.stderr.strip()}")
    return result.stdout.strip()


def validate_origin(origin):
    """Allow normal local/SSH origins, never freeze URL credentials or query/fragment data."""
    invalid = not isinstance(origin, str) or not origin or any(ord(c) < 32 for c in origin)
    try:
        parsed = urlsplit(origin) if not invalid else None
        if parsed is not None:
            invalid = bool(parsed.query or parsed.fragment or "?" in origin or "#" in origin)
            if parsed.scheme.lower() in {"http", "https"}:
                invalid |= not parsed.netloc or parsed.username is not None or not parsed.hostname
                invalid |= parsed.port is not None and parsed.port <= 0
            elif "://" in origin:
                invalid |= parsed.password is not None or (parsed.username is not None and parsed.scheme != "ssh")
            elif "@" in origin:
                invalid |= re.fullmatch(r"[^:@/]+@[^:/]+:.+", origin) is None
    except ValueError:
        invalid = True
    if invalid:
        raise ValueError("optional Git origin must be credential-free with no query or fragment")
    return origin


def identity(root, *, runner=None, allow_uncommitted=False):
    root = checked_directory(root)
    dotgit = root / ".git"
    if dotgit.is_symlink() or not dotgit.is_dir():
        raise ValueError(f"optional source must be an independent Git directory: {root}")
    if any((dotgit / name).is_symlink() for name in ("config", "HEAD", "objects")):
        raise ValueError(f"optional Git identity/object symlinks are forbidden: {root}")
    # Configuration output can contain credentials: bypass the runner/log entirely.
    origin = validate_origin(git(root, "config", "--get-all", "remote.origin.url", confidential=True))
    top = git(root, "rev-parse", "--show-toplevel", runner=runner)
    common = git(root, "rev-parse", "--path-format=absolute", "--git-common-dir", runner=runner)
    if Path(top) != root or Path(common) != dotgit:
        raise ValueError(f"optional Git worktree/shared repository is forbidden: {root}")
    if (dotgit / "objects/info/alternates").exists() or (dotgit / "commondir").exists():
        raise ValueError(f"optional Git object sharing is forbidden: {root}")
    # --revs-only returns an empty string for a newly initialized, unborn HEAD.
    revision = git(root, "rev-parse", "--revs-only", "HEAD", runner=runner)
    if not re.fullmatch(r"[0-9a-f]{40,64}", revision):
        if not allow_uncommitted:
            raise ValueError(f"optional source Git HEAD has no commit: {root}")
        revision = "uncommitted"
    return {"origin": origin, "revision": revision}


def repository_name(origin):
    value = origin.rstrip("/").rsplit("/", 1)[-1].rsplit(":", 1)[-1]
    value = value[:-4] if value.endswith(".git") else value
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]*", value):
        raise ValueError("main Git origin must identify a repository basename")
    return value


def enabled(config):
    if "optional_packages" not in config:
        return False
    value = config["optional_packages"]
    if (not isinstance(value, dict) or set(value) != {"schema"}
            or type(value.get("schema")) is not int or value["schema"] != 1):
        raise ValueError("fnpackkit.optional_packages must be {\"schema\": 1}")
    return True
