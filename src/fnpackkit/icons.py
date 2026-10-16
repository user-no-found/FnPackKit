"""Map project-owned PNG branding to FPK icon locations and validate staged icons."""
from dataclasses import replace
from pathlib import Path

from .config_checks import object_value, text_value
from .contracts import PayloadFile


MAX_ICON_BYTES = 1024 * 1024


def validate_icon(path, size=None):
    """Validate PNG icons fully; decode existing JPEG icons without changing them."""
    if path.is_symlink() or not path.is_file() or path.stat().st_size > MAX_ICON_BYTES:
        raise ValueError(f"icon missing, nonregular or exceeds 1 MiB: {path}")
    with path.open("rb") as stream:
        signature = stream.read(8)
    if signature == b"\x89PNG\r\n\x1a\n":
        from buildflow.images import inspect_png
        info = inspect_png(path, max_bytes=MAX_ICON_BYTES)
        if info["color_space"] != "srgb":
            raise ValueError(f"icon must use confirmed sRGB: {path}")
        width, height = info["width"], info["height"]
    elif signature.startswith(b"\xff\xd8\xff"):
        try:
            from PIL import Image
        except ImportError as exc:
            raise ValueError("icon validation requires BuildFlow's images extra (Pillow)") from exc
        with Image.open(path) as image:
            if image.format != "JPEG" or image.width * image.height > 16 * 1024 * 1024:
                raise ValueError(f"invalid or excessive JPEG icon: {path}")
            image.load()
            width, height = image.size
    else:
        raise ValueError(f"icon must contain PNG or JPEG image data: {path}")
    if width != height or (size is not None and width != size):
        raise ValueError(f"icon must be square{f' at {size}px' if size else ''}: {path}")


def prepare_payload_icons(project, payload, work):
    """Derive optional branding without overwriting template or payload mappings."""
    branding = object_value(project.data.get("branding", {}), "branding")
    if "icon" not in branding:
        return payload, None
    icon = object_value(branding["icon"], "branding.icon")

    def source(value):
        value = Path(text_value(value, "branding icon path"))
        return value if value.is_absolute() else project.root / value

    variants = object_value(icon.get("variants", {}), "branding.icon.variants")
    if set(variants) - {"64", "256"}:
        raise ValueError("FPK branding variants must have size keys 64 or 256")
    from buildflow.images import prepare_icons
    prepared = prepare_icons(source(icon.get("source")), work, png_sizes=(64, 256),
                             variants={int(key): source(value) for key, value in variants.items()})
    mappings = ((64, "ICON.PNG"), (256, "ICON_256.PNG"),
                (64, "app/ui/images/icon_64.png"), (256, "app/ui/images/icon_256.png"))
    files = list(payload.files)
    occupied = {entry.destination for entry in files}
    for size, destination in mappings:
        if destination in occupied or (Path(payload.template) / destination).exists():
            raise ValueError(f"branding icon mapping conflicts with existing content: {destination}")
        path = prepared["png"][size]
        validate_icon(path, size)
        files.append(PayloadFile(path, destination, 0o644))
    return replace(payload, files=files), prepared["metadata"]
