"""Verify the documented fnpack archive transformations against staged inputs."""
import hashlib
import tarfile

from .archive import inspect_fpk, safe_relative
from .formats import manifest_fields


def verify_package(path, stage, expected):
    """Account for fnpack's manifest checksum and mirrored config only."""
    outer, inner = inspect_fpk(path)
    actual = {name: entry for name, entry in outer.items() if name != "app.tgz"}
    actual.update({"app/" + name: entry for name, entry in inner.items()})
    expected = dict(expected)
    # fnpack mirrors these package resource declarations into the app payload.
    for name in ("config/resource", "config/privilege"):
        if name in expected:
            mirrored = "app/" + name
            if mirrored in expected and expected[mirrored] != expected[name]:
                raise ValueError(f"conflicting package and app config: {name}")
            expected[mirrored] = expected[name]
    expected_manifest = manifest_fields((stage / "manifest").read_text())
    with tarfile.open(path, "r:*") as archive:
        members = {safe_relative(item.name): item for item in archive.getmembers()}
        if members["manifest"].size > 65536:
            raise ValueError("packaged manifest exceeds 64 KiB")
        with archive.extractfile(members["manifest"]) as stream:
            packaged_manifest = manifest_fields(stream.read().decode("utf-8"))
        with archive.extractfile(members["app.tgz"]) as stream:
            native_checksum = hashlib.file_digest(stream, "md5").hexdigest()
    if packaged_manifest.pop("checksum", None) != native_checksum:
        raise ValueError("fnpack manifest checksum does not match app.tgz")
    for field in ("desktop_uidir", "desktop_applaunchname"):
        if field not in expected_manifest and packaged_manifest.get(field) == "":
            packaged_manifest.pop(field)
    expected_manifest.pop("checksum", None)
    if packaged_manifest != expected_manifest:
        raise ValueError("packaged manifest changes declared metadata")
    if actual["manifest"]["mode"] != expected["manifest"]["mode"]:
        raise ValueError("packaged manifest mode changed")
    expected["manifest"] = actual["manifest"]
    if actual != expected:
        missing = sorted(set(expected) - set(actual))
        extra = sorted(set(actual) - set(expected))
        changed = sorted(name for name in set(actual) & set(expected) if actual[name] != expected[name])
        raise ValueError(f"actual FPK differs from staged payload: missing={missing}, extra={extra}, changed={changed}")
    return actual
