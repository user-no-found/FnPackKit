"""Verify content-addressed optional selection records against original and frozen inputs."""
import json
from pathlib import Path

from .optional_contracts import identity, json_bytes, read_regular, sha256


def fingerprint(record):
    selected = []
    for item in record["selected"]:
        selected.append({key: item[key] for key in ("id", "origin", "revision", "manifest_sha256")}
                        | {"inputs": [{"path": entry["path"], "sha256": entry["sha256"]}
                                      for entry in item["inputs"]]})
    return sha256(json_bytes({"schema": 1, "project": record["project"], "selected": selected}))


def validate_selection(record, *, runner=None):
    """Reject changed selected repositories or frozen bytes (local Git only, no network)."""
    if not isinstance(record, dict) or record.get("schema") != 1:
        raise ValueError("invalid frozen optional selection schema")
    if record.get("fingerprint") != fingerprint(record):
        raise ValueError("frozen optional selection fingerprint changed")
    for item in record["selected"]:
        path = Path(item["path"])
        current = identity(path, runner=runner, allow_uncommitted=True)
        if current != {key: item[key] for key in ("origin", "revision")}:
            raise ValueError(f"selected optional Git identity changed: {item['id']}")
        original = read_regular(path, "fpk-option.json", limit=1024 * 1024)
        snapshot = Path(item["manifest_snapshot"])
        frozen = read_regular(snapshot.parent, snapshot.name, limit=1024 * 1024)
        if (sha256(original) != item["manifest_sha256"] or frozen != original
                or json.loads(frozen) != item["manifest"]):
            raise ValueError(f"selected optional manifest changed: {item['id']}")
        for entry in item["inputs"]:
            frozen = Path(entry["snapshot"])
            if (sha256(read_regular(path, entry["path"])) != entry["sha256"]
                    or sha256(read_regular(frozen.parent, frozen.name)) != entry["sha256"]):
                raise ValueError(f"selected optional input changed: {item['id']}/{entry['path']}")
    return record
