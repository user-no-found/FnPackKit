"""Render application templates and copy regular files into a private package stage."""
import hashlib
from contextlib import nullcontext
from pathlib import Path
import re
import shutil

from .archive import safe_relative
from .publisher import apply_profile


TOKEN = re.compile(r"@([A-Z][A-Z0-9_]*)@")


def digest(path):
    with Path(path).open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def regular_tree(root):
    root = Path(root)
    if root.is_symlink() or not root.is_dir():
        raise ValueError(f"expected a regular directory: {root}")
    for path in sorted(root.rglob("*")):
        if path.is_symlink() or not (path.is_file() or path.is_dir()):
            raise ValueError(f"symlink or special file rejected: {path}")
        if path.stat().st_mode & 0o7000:
            raise ValueError(f"special permission bits rejected: {path}")
        yield path


def render(text, values):
    def replace(match):
        name = match.group(1)
        if name not in values:
            raise ValueError(f"missing template value: {name}")
        return values[name]
    result = TOKEN.sub(replace, text)
    if TOKEN.search(result):
        raise ValueError("unresolved nested template token")
    return result


def _copy_templates(payload, stage):
    source = Path(payload.template)
    paths = list(regular_tree(source))
    stage.mkdir(parents=True)
    for path in paths:
        target = stage / path.relative_to(source)
        if path.is_dir():
            target.mkdir(exist_ok=True)
        else:
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(path, target)


def _render_templates(payload, stage):
    for path in sorted(stage.rglob("*.in")):
        if not path.is_file():
            continue
        target = path.with_suffix("")
        if target.exists():
            raise ValueError(f"template target already exists: {target}")
        target.write_text(render(path.read_text(), payload.values))
        target.chmod(path.stat().st_mode & 0o777)
        path.unlink()


def _copy_payload(payload, stage):
    for entry in payload.files:
        source = Path(entry.source)
        if source.is_symlink() or not source.is_file() or source.stat().st_mode & 0o7000:
            raise ValueError(f"payload requires regular file: {source}")
        target = stage / safe_relative(entry.destination)
        if target.exists():
            raise ValueError(f"payload would overwrite template content: {target}")
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(source, target)
        if entry.mode is not None:
            target.chmod(entry.mode)


def _validate_stage(payload, stage):
    for name in ["manifest", *payload.required]:
        if not (stage / safe_relative(name)).is_file():
            raise ValueError(f"required staged file missing: {name}")
    if not (stage / "app").is_dir():
        raise ValueError("package requires an app directory")
    manifest = apply_profile(stage / "manifest")
    for field, token in {"appname": "APP_ID", "version": "VERSION", "platform": "PLATFORM"}.items():
        if manifest.get(field) != payload.values[token]:
            raise ValueError(f"rendered manifest {field} differs from payload metadata")
    return {path.relative_to(stage).as_posix(): {"sha256": digest(path),
            "mode": path.stat().st_mode & 0o777, "size": path.stat().st_size}
            for path in regular_tree(stage) if path.is_file()}


def create_stage(payload, stage, *, activity=None):
    """Stage a payload, optionally reporting through the caller's shared activity API."""
    span = activity or (lambda label: nullcontext())
    with span("复制封包模板"):
        _copy_templates(payload, stage)
    with span("渲染封包模板"):
        _render_templates(payload, stage)
    with span("复制应用文件"):
        _copy_payload(payload, stage)
    with span("核对文件清单与摘要"):
        return _validate_stage(payload, stage)
