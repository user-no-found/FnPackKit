"""Validate known fnOS configuration fields while preserving extension fields."""
import json
import re

from .archive import safe_relative


def object_value(value, label):
    if not isinstance(value, dict):
        raise ValueError(f"{label} must be an object")
    return value


def text_value(value, label):
    if not isinstance(value, str) or not value or "\x00" in value:
        raise ValueError(f"{label} must be a nonempty string")
    return value


def string_list(value, label):
    if not isinstance(value, list):
        raise ValueError(f"{label} must be an array of strings")
    for item in value:
        text_value(item, label)
    return value


def read_json(path):
    """Read bounded JSON without silently accepting duplicate keys."""
    def pairs(items):
        result = {}
        for key, value in items:
            if key in result:
                raise ValueError(f"duplicate JSON key {key!r}: {path}")
            result[key] = value
        return result
    if not path.is_file() or path.stat().st_size > 1024 * 1024:
        raise ValueError(f"missing or excessive configuration file: {path}")
    return json.loads(path.read_text(encoding="utf-8"), object_pairs_hook=pairs)


def relative(value, label):
    text_value(value, label)
    return safe_relative(value)


def validate_privilege(data):
    object_value(data, "privilege")
    defaults = object_value(data.get("defaults", {}), "privilege.defaults")
    if "run-as" in defaults and defaults["run-as"] not in ("root", "package"):
        raise ValueError("privilege.defaults.run-as must be root or package")
    for key in ("username", "groupname"):
        if key in data:
            text_value(data[key], f"privilege.{key}")
    if "join-groups" in data:
        string_list(data["join-groups"], "privilege.join-groups")


def validate_resource(data, app):
    object_value(data, "resource")
    if "api-scope" in data:
        string_list(data["api-scope"], "resource.api-scope")
    if "data-share" in data:
        share = object_value(data["data-share"], "resource.data-share")
        shares = share.get("shares", [])
        if not isinstance(shares, list):
            raise ValueError("resource.data-share.shares must be an array")
        for item in shares:
            object_value(item, "data-share entry")
            relative(item.get("name"), "data-share.name")
            permission = object_value(item.get("permission", {}), "data-share.permission")
            for key in ("rw", "ro"):
                if key in permission:
                    string_list(permission[key], f"data-share.permission.{key}")
    if "usr-local-linker" in data:
        linker = object_value(data["usr-local-linker"], "resource.usr-local-linker")
        for key in ("bin", "lib", "etc"):
            for value in string_list(linker.get(key, []), f"usr-local-linker.{key}"):
                path = app / relative(value, f"usr-local-linker.{key}")
                if not path.exists():
                    raise ValueError(f"usr-local-linker target missing: {value}")
    if "docker-project" in data:
        docker = object_value(data["docker-project"], "resource.docker-project")
        projects = docker.get("projects", [])
        if not isinstance(projects, list):
            raise ValueError("docker-project.projects must be an array")
        for item in projects:
            object_value(item, "docker-project entry")
            text_value(item.get("name"), "docker-project.name")
            path = app / relative(item.get("path"), "docker-project.path")
            if not (path / "docker-compose.yaml").is_file():
                raise ValueError("docker-project.path requires docker-compose.yaml")


def validate_wizard(root):
    for name in ("install", "upgrade", "uninstall", "config"):
        path = root / name
        if not path.exists():
            continue
        steps = read_json(path)
        if not isinstance(steps, list):
            raise ValueError(f"wizard/{name} must be an array of steps")
        for step in steps:
            object_value(step, "wizard step")
            text_value(step.get("stepTitle"), "wizard.stepTitle")
            items = step.get("items")
            if not isinstance(items, list):
                raise ValueError("wizard.items must be an array")
            for item in items:
                object_value(item, "wizard item")
                text_value(item.get("type"), "wizard.type")
                if "field" in item:
                    field = text_value(item["field"], "wizard.field")
                    if field.startswith("TRIM_") or not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", field):
                        raise ValueError("wizard.field must be an environment name without TRIM_ prefix")
                for key in ("label", "helpText"):
                    if key in item and not isinstance(item[key], str):
                        raise ValueError(f"wizard.{key} must be a string")
                for key in ("options", "rules"):
                    if key in item:
                        if not isinstance(item[key], list):
                            raise ValueError(f"wizard.{key} must be an array")
                        for entry in item[key]:
                            object_value(entry, f"wizard.{key} entry")
                for option in item.get("options", []):
                    text_value(option.get("label"), "wizard.option.label")
                    if "value" not in option:
                        raise ValueError("wizard.option requires value")


def validate_capabilities(capabilities):
    """Validate only explicitly supplied SDK and backend API declarations."""
    object_value(capabilities, "fnpackkit.capabilities")
    unknown = set(capabilities) - {"js_sdk", "api_scopes"}
    if unknown:
        raise ValueError(f"unknown fnpackkit.capabilities fields: {sorted(unknown)}")
    if "js_sdk" in capabilities and type(capabilities["js_sdk"]) is not bool:
        raise ValueError("fnpackkit.capabilities.js_sdk must be boolean")
    if "api_scopes" in capabilities:
        string_list(capabilities["api_scopes"], "fnpackkit.capabilities.api_scopes")


def check_capabilities(capabilities, manifest, resource):
    validate_capabilities(capabilities)
    if capabilities.get("js_sdk") and manifest.get("micro_app") != "true":
        raise ValueError("declared js_sdk requires manifest micro_app=true")
    missing = set(capabilities.get("api_scopes", [])) - set(resource.get("api-scope", []))
    if missing:
        raise ValueError(f"resource.api-scope missing declared backend scopes: {sorted(missing)}")
