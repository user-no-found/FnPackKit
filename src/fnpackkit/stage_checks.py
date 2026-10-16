"""Check manifest, desktop references and declared package capabilities."""
import re
from urllib.parse import unquote, urlsplit

from .config_checks import (check_capabilities, object_value, read_json, relative, string_list,
                            text_value, validate_privilege, validate_resource, validate_wizard)
from .icons import validate_icon


def _gateway(entry, app_id):
    prefix = text_value(entry.get("gatewayPrefix"), "gatewayPrefix")
    base = "/app/" + app_id
    if (not (prefix == base or prefix.startswith(base + "/"))
            or any(not re.fullmatch(r"[A-Za-z0-9._~-]+", part) or part in (".", "..")
                   for part in prefix[1:].split("/"))):
        raise ValueError("gatewayPrefix must be a safe /app/{appname} path")
    socket = text_value(entry.get("gatewaySocket"), "gatewaySocket")
    if socket in (".", "..") or any(char in "/\\" or ord(char) < 32 or ord(char) == 127 for char in socket):
        raise ValueError("gatewaySocket must be a filename")
    url = text_value(entry.get("url"), "gateway url")
    parsed = urlsplit(url)
    path = unquote(parsed.path)
    if (parsed.scheme or parsed.netloc or not (path == prefix or path.startswith(prefix + "/"))
            or "\\" in path or any(part in (".", "..") for part in path.split("/"))):
        raise ValueError("gateway url must stay within gatewayPrefix")


def _desktop(stage, manifest):
    app_id = manifest["appname"]
    declared = manifest.get("desktop_uidir")
    default_config = stage / "app/ui/config"
    if not declared and not manifest.get("desktop_applaunchname") and not default_config.exists():
        return
    ui = stage / "app" / relative(declared or "ui", "desktop_uidir")
    config = object_value(read_json(ui / "config"), "UI config")
    entries = object_value(config.get(".url", {}), "UI .url")
    launcher = manifest.get("desktop_applaunchname")
    if launcher and launcher not in entries:
        raise ValueError("desktop_applaunchname does not name a UI .url entry")
    for name, entry in entries.items():
        if not name.startswith(app_id + "."):
            raise ValueError("UI entry ID must use the appname prefix")
        object_value(entry, f"UI entry {name}")
        text_value(entry.get("title"), "UI title")
        if entry.get("type") not in ("iframe", "url"):
            raise ValueError("UI type must be iframe or url")
        for key in ("allUsers", "noDisplay"):
            if key in entry and type(entry[key]) is not bool:
                raise ValueError(f"UI {key} must be boolean")
        if "fileTypes" in entry:
            string_list(entry["fileTypes"], "UI fileTypes")
        if "control" in entry:
            control = object_value(entry["control"], "UI control")
            if "accessPerm" in control and control["accessPerm"] not in ("editable", "readonly", "hidden"):
                raise ValueError("UI control.accessPerm is invalid")
        icon = relative(entry.get("icon"), "UI icon")
        if "{0}" in icon:
            for size in (64, 256):
                validate_icon(ui / icon.replace("{0}", str(size)), size)
        else:
            validate_icon(ui / icon)
        if "gatewayPrefix" in entry or "gatewaySocket" in entry:
            _gateway(entry, app_id)
        else:
            url = text_value(entry.get("url"), "UI url")
            cgi = f"/cgi/ThirdParty/{app_id}/index.cgi"
            if urlsplit(url).path == cgi or urlsplit(url).path.startswith(cgi + "/"):
                continue
            if entry.get("protocol", "") not in ("", "http", "https"):
                raise ValueError("UI protocol must be http, https or empty")
            if "port" in entry and entry["port"] != "":
                port = entry["port"]
                if not (isinstance(port, str) and re.fullmatch(r"\$\{[A-Za-z_][A-Za-z0-9_]*\}", port)):
                    if isinstance(port, bool) or not str(port).isdigit() or not 1 <= int(port) <= 65535:
                        raise ValueError("UI port must be 1..65535 or a wizard variable")


def validate_stage_contract(stage, manifest, capabilities=None):
    """Validate static declarations; runtime and device behavior remain application-owned."""
    if manifest.get("platform") not in ("x86", "arm", "all"):
        raise ValueError("manifest platform must be x86, arm or all")
    if manifest.get("source") != "thirdparty":
        raise ValueError("manifest source must be thirdparty")
    for key in ("ctl_stop", "checkport", "micro_app"):
        if key in manifest and manifest[key] not in ("true", "false"):
            raise ValueError(f"manifest {key} must be true or false")
    for size, name in ((64, "ICON.PNG"), (256, "ICON_256.PNG")):
        validate_icon(stage / name, size)
    privilege = read_json(stage / "config/privilege")
    resource = read_json(stage / "config/resource")
    validate_privilege(privilege)
    validate_resource(resource, stage / "app")
    validate_wizard(stage / "wizard")
    _desktop(stage, manifest)
    check_capabilities(capabilities or {}, manifest, resource)
