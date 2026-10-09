"""Validate FPK project declarations and application payload manifests."""
import json
from pathlib import Path
import re

from buildflow.project import check_requirement
from .archive import safe_relative
from .contracts import Payload, PayloadFile
from .metadata import tool_version


def validate_project(project):
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]*", project.data["app_id"]):
        raise ValueError("FPK app_id must be a safe package identifier")
    config = project.data.get("fnpackkit")
    if not isinstance(config, dict) or type(config.get("schema")) is not int or config["schema"] != 1:
        raise ValueError("fnpackkit.schema must be 1")
    if "fpk" not in project.config["targets"]:
        raise ValueError("project does not declare buildflow target fpk")
    unknown = set(config) - {"schema", "requires", "payload", "adapter"}
    if unknown:
        raise ValueError(f"unknown fnpackkit fields: {sorted(unknown)}")
    check_requirement(config.get("requires", ""), tool_version())
    if bool(config.get("payload")) == bool(config.get("adapter")):
        raise ValueError("declare exactly one of fnpackkit.payload or fnpackkit.adapter")
    for key in ("payload", "adapter"):
        if key in config and (not isinstance(config[key], str) or not config[key]):
            raise ValueError(f"fnpackkit.{key} must be a nonempty string")
    return config


def read_payload(path):
    path = Path(path)
    if path.stat().st_size > 1024 * 1024:
        raise ValueError("payload manifest exceeds 1 MiB")
    data = json.loads(path.read_text())
    if not isinstance(data, dict) or type(data.get("schema")) is not int or data["schema"] != 1:
        raise ValueError("payload.schema must be 1")
    unknown = set(data) - {"schema", "template", "files", "values", "artifact_name", "metadata", "required"}
    if unknown:
        raise ValueError(f"unknown payload fields: {sorted(unknown)}")
    def source(value):
        value = Path(value)
        return value if value.is_absolute() else path.parent / value
    files = []
    for entry in data["files"]:
        if not isinstance(entry, dict) or set(entry) - {"source", "destination", "mode"}:
            raise ValueError("invalid payload file declaration")
        files.append(PayloadFile(source(entry["source"]), entry["destination"], entry.get("mode")))
    return Payload(source(data["template"]), files, data["values"], data["artifact_name"],
                   data.get("metadata", {}), data.get("required", []))


def validate_payload(payload, app_id):
    if not isinstance(payload, Payload):
        raise ValueError("adapter must return Payload declarations")
    if (not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]*\.fpk", payload.artifact_name)
            or payload.artifact_name.startswith(".")):
        raise ValueError("artifact_name must be one .fpk filename")
    if not isinstance(payload.values, dict):
        raise ValueError("payload values must be a mapping")
    for name, value in payload.values.items():
        if (not isinstance(name, str) or not re.fullmatch(r"[A-Z][A-Z0-9_]*", name)
                or not isinstance(value, str) or any(char in value for char in "\r\n\x00")):
            raise ValueError(f"invalid template value: {name!r}")
    for name in ("APP_ID", "VERSION", "PLATFORM"):
        if not payload.values.get(name):
            raise ValueError(f"payload requires {name}")
    if payload.values["APP_ID"] != app_id:
        raise ValueError("payload APP_ID must equal project app_id")
    destinations = set()
    for entry in payload.files:
        name = safe_relative(entry.destination)
        if name in destinations:
            raise ValueError(f"duplicate payload destination: {name}")
        destinations.add(name)
        if entry.mode is not None and (type(entry.mode) is not int or entry.mode < 0 or entry.mode & ~0o777):
            raise ValueError("payload mode must contain only permission bits")
    for name in payload.required:
        safe_relative(name)
    if not isinstance(payload.metadata, dict):
        raise ValueError("payload metadata must be an object")
