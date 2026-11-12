"""Apply only frozen optional text patches and new files to an application-owned stage."""
from pathlib import Path
import tempfile

from .optional_contracts import checked_path, git, identity, read_regular
from .optional_patch import patch_plan
from .optional_snapshot import validate_selection
from .optional_transaction import commit, state


def _operations(record):
    patches, files, owners, file_targets = [], [], {}, set()
    declarations = {package["id"]: set(package["manifest"].get("shared_patch_targets", []))
                    for package in record["selected"]}
    for package in record["selected"]:
        manifest = package["manifest"]
        shared = declarations[package["id"]]
        touched = set()
        inputs = {entry["path"]: entry for entry in package["inputs"]}
        for name in manifest["patches"]:
            entry = inputs[name]
            snapshot = Path(entry["snapshot"])
            content = read_regular(snapshot.parent, snapshot.name)
            paths = patch_plan(content, shared_targets=shared)
            for target in paths:
                if target in file_targets:
                    raise ValueError(f"optional file/patch destination collision: {target}")
                previous = owners.setdefault(target, set())
                if previous - {package["id"]} and (target not in shared or any(
                        target not in declarations[owner] for owner in previous)):
                    raise ValueError(f"optional patch collision between packages: {target}")
                previous.add(package["id"])
            touched.update(paths)
            patches.append((snapshot, paths, shared.intersection(paths)))
        if shared - touched:
            raise ValueError(f"optional shared_patch_targets not modified by package: {', '.join(sorted(shared - touched))}")
        for entry in manifest["files"]:
            name = entry["destination"]
            if name in owners:
                raise ValueError(f"optional file/patch destination collision: {name}")
            owners[name] = {package["id"]}
            file_targets.add(name)
            source = Path(inputs[entry["source"]]["snapshot"])
            files.append((name, read_regular(source.parent, source.name), entry.get("mode", 0o644)))
    names = set(owners)
    for name in names:
        if any(parent.as_posix() in names for parent in Path(name).parents):
            raise ValueError(f"optional target file/directory collision: {name}")
    return patches, files, sorted(names)


def _text_state(root, name):
    value = state(root, name)
    if value is None:
        raise ValueError(f"optional shared patch target must be an existing regular UTF-8 text file: {name}")
    try:
        value[0].decode("utf-8")
    except UnicodeDecodeError as exc:
        raise ValueError(f"optional shared patch target must be UTF-8 text: {name}") from exc
    if b"\0" in value[0]:
        raise ValueError(f"optional shared patch target must not contain binary NUL data: {name}")
    return value


def apply_source(ctx, target, *, source_origin=None, source_revision=None):
    """Preflight the complete selection, then transactionally change only a private Git target.

    Baseline tracked/untracked changes are allowed. The caller owns this stage exclusively;
    its unchanged Git HEAD/origin describe the upstream identity, not the projected content.
    """
    record = ctx.metadata.get("optional_packages")
    if record is None:
        raise ValueError("optional selection step has not run")
    with ctx.activity("复核可选功能源"):
        validate_selection(record, runner=ctx.runner)
    target = Path(target).absolute()
    work = ctx.work.absolute()
    if target == work or not target.is_relative_to(work):
        raise ValueError("optional target must be a private Git checkout inside ctx.work")
    checked_path(work, target.relative_to(work).as_posix())
    source = identity(target, runner=ctx.runner)
    expected = {"origin": source_origin, "revision": source_revision}
    for key, value in expected.items():
        if value is not None and value != source[key]:
            raise ValueError(f"optional target source {key} does not match caller identity")
    for package in record["selected"]:
        for key, value in package["manifest"].get("source", {}).items():
            if source[key] != value:
                raise ValueError(f"optional source {key} constraint failed: {package['id']}")
        package_root = Path(package["path"])
        if target.is_relative_to(package_root) or package_root.is_relative_to(target):
            raise ValueError("optional target overlaps an optional repository")
    patches, files, names = _operations(record)
    before = {name: state(target, name) for name in names}
    for name, _, _ in files:
        if before[name] is not None:
            raise ValueError(f"optional file destination already exists: {name}")
    with tempfile.TemporaryDirectory(prefix="optional-preflight-", dir=work) as directory:
        scratch = Path(directory)
        git(scratch, "init", "--quiet", runner=ctx.runner)
        for name, value in before.items():
            if value is not None:
                path = scratch / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(value[0])
                path.chmod(value[1])
        for snapshot, paths, shared in patches:
            shared_before = {name: _text_state(scratch, name) for name in shared}
            statistics = git(scratch, "apply", "--numstat", "-z", str(snapshot), runner=ctx.runner)
            actual = [entry.split("\t", 2)[2] for entry in statistics.split("\0") if entry]
            if sorted(actual) != sorted(paths):
                raise ValueError("optional patch contains undeclared or ambiguous target paths")
            git(scratch, "apply", "--check", str(snapshot), runner=ctx.runner)
            git(scratch, "apply", str(snapshot), runner=ctx.runner)
            for name, mode in paths.items():
                if mode is not None and (scratch / name).exists():
                    (scratch / name).chmod(mode)
            for name, previous in shared_before.items():
                current = _text_state(scratch, name)
                if current[0] == previous[0]:
                    raise ValueError(f"optional shared patch target requires a content modification: {name}")
                # Text-only Git rewrites can apply the process umask; preserve exact baseline permissions.
                (scratch / name).chmod(previous[1])
        for name, content, mode in files:
            path = scratch / name
            if path.exists() or path.is_symlink():
                raise ValueError(f"optional file destination collision: {name}")
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(content)
            path.chmod(mode)
        after = {name: state(scratch, name) for name in names}
        validate_selection(record, runner=ctx.runner)
        if identity(target, runner=ctx.runner) != source:
            raise ValueError("optional target Git identity changed during preflight")
        commit(target, before, after)
    return {"fingerprint": record["fingerprint"], "source": source, "paths": names}
