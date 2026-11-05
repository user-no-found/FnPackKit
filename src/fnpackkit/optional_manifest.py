"""Statically discover and validate one-level declarative optional packages."""
import json
import re

from .optional_contracts import (ID, MAX_TOTAL, checked_path, identity, read_regular,
                                 relative, repository_name, sha256, validate_origin)


def _object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate optional manifest key: {key}")
        result[key] = value
    return result


def parse(content, *, project, directory):
    try:
        data = json.loads(content, object_pairs_hook=_object)
    except (UnicodeError, json.JSONDecodeError) as exc:
        raise ValueError(f"invalid optional manifest in {directory}: {exc}") from exc
    required = {"schema", "id", "label", "project", "patches", "files"}
    allowed = required | {"source", "tests", "shared_patch_targets"}
    if not isinstance(data, dict) or set(data) - allowed or required - set(data):
        raise ValueError(f"optional manifest accepts only {','.join(sorted(allowed))}: {directory}")
    if type(data["schema"]) is not int or data["schema"] != 1:
        raise ValueError(f"optional manifest schema must be 1: {directory}")
    if not isinstance(data["id"], str) or not ID.fullmatch(data["id"]):
        raise ValueError(f"invalid optional feature id: {data['id']!r}")
    label = data["label"]
    if not isinstance(label, str) or not label.strip() or len(label) > 120 or any(ord(c) < 32 for c in label):
        raise ValueError("optional label must be one nonempty plain-text line (at most 120 characters)")
    if data["project"] != project:
        raise ValueError(f"optional project must equal main origin repository {project}: {directory}")
    for key in ("patches", "files", "tests", "shared_patch_targets"):
        if not isinstance(data.get(key, []), list) or len(data.get(key, [])) > 4096:
            raise ValueError(f"optional {key} must be a bounded array")
    shared = [relative(name) for name in data.get("shared_patch_targets", [])]
    if len(shared) != len(set(shared)):
        raise ValueError("duplicate optional shared_patch_targets")
    destinations = []
    for name in (*data["patches"], *data.get("tests", [])):
        relative(name)
    for entry in data["files"]:
        if (not isinstance(entry, dict) or set(entry) - {"source", "destination", "mode"}
                or not {"source", "destination"} <= set(entry)):
            raise ValueError("optional files require source/destination and optional mode only")
        relative(entry["source"])
        destinations.append(relative(entry["destination"]))
        if type(entry.get("mode", 0o644)) is not int or entry.get("mode", 0o644) not in {0o644, 0o755}:
            raise ValueError("optional file mode must be 0644 or 0755")
    if (len(data["patches"]) != len(set(data["patches"]))
            or len(data.get("tests", [])) != len(set(data.get("tests", [])))
            or len(destinations) != len(set(destinations))):
        raise ValueError("duplicate optional patch/test or destination")
    constraints = data.get("source", {})
    if not isinstance(constraints, dict) or set(constraints) - {"origin", "revision"}:
        raise ValueError("optional source constraints accept only origin/revision")
    for key, value in constraints.items():
        if not isinstance(value, str) or not value or any(ord(c) < 32 for c in value):
            raise ValueError(f"invalid optional source constraint: {key}")
        if key == "origin":
            validate_origin(value)
        if key == "revision" and not re.fullmatch(r"[0-9a-f]{40,64}", value):
            raise ValueError("optional source revision constraint must be a full Git commit")
    return data


def scan(project, *, runner=None):
    """Read only direct regular repositories with fpk-option.json; never import code."""
    root = project.root.absolute()
    optional = root / "optional"
    if not optional.exists() and not optional.is_symlink():
        return None, []
    checked_path(root, "optional")
    if not optional.is_dir():
        raise ValueError("optional/ must be a regular directory")
    entries = sorted(optional.iterdir())
    if len(entries) > 4096:
        raise ValueError("optional directory exceeds 4096 entries")
    candidates = []
    project_name = None
    seen = set()
    total = 0
    for path in entries:
        if path.is_symlink():
            raise ValueError(f"optional symlink directory is forbidden: {path.name}")
        if not path.is_dir():
            continue
        manifest = path / "fpk-option.json"
        if not manifest.exists() and not manifest.is_symlink():
            continue
        if project_name is None:
            project_name = repository_name(identity(root, runner=runner, allow_uncommitted=True)["origin"])
        content = read_regular(path, "fpk-option.json", limit=1024 * 1024)
        data = parse(content, project=project_name, directory=path.name)
        if data["id"] in seen:
            raise ValueError(f"duplicate optional feature id: {data['id']}")
        seen.add(data["id"])
        expected = project_name + "Optional-" + data["id"]
        if path.name != expected:
            raise ValueError(f"optional directory/id mismatch: expected {expected}, got {path.name}")
        info = identity(path, runner=runner, allow_uncommitted=True)
        if repository_name(info["origin"]) != path.name:
            raise ValueError(f"optional origin basename must equal directory {path.name}")
        contents = {}
        total += len(content)
        for name in dict.fromkeys((*data["patches"], *(entry["source"] for entry in data["files"]), *data.get("tests", []))):
            contents[name] = read_regular(path, name)
            total += len(contents[name])
            if total > MAX_TOTAL:
                raise ValueError("optional declared input budget exceeds 128 MiB")
        candidates.append({"path": str(path), "manifest": data, "manifest_content": content,
                           "contents": contents, **info})
    return project_name, candidates


def freeze(candidate, work):
    """Store immutable-by-evidence copies; validators rehash both originals and copies."""
    data = candidate["manifest"]
    destination = checked_path(work, "optional-inputs/" + data["id"], missing=True)
    destination.mkdir(parents=True, exist_ok=False)
    (destination / "manifest.json").write_bytes(candidate["manifest_content"])
    inputs = []
    for index, (name, content) in enumerate(candidate["contents"].items()):
        snapshot = destination / str(index)
        snapshot.write_bytes(content)
        inputs.append({"path": name, "sha256": sha256(content), "snapshot": str(snapshot)})
    return {"id": data["id"], "label": data["label"], "path": candidate["path"],
            "origin": candidate["origin"], "revision": candidate["revision"],
            "manifest": data, "manifest_sha256": sha256(candidate["manifest_content"]),
            "manifest_snapshot": str(destination / "manifest.json"), "inputs": inputs}
