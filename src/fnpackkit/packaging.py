"""FPK-specific operations executed through a caller's BuildFlow context."""
import json
from pathlib import Path
import platform
import re
import sys

from buildflow.contracts import Artifact
from .verification import verify_package
from .manifest import validate_payload
from .metadata import tool_version
from .staging import create_stage, digest
from .icons import prepare_payload_icons


_FNPACK_EXEC = "import os,sys; os.umask(0o022); os.execvp(sys.argv[1], sys.argv[1:])"


def package(ctx, payload, fnpack="fnpack"):
    """Render, build and verify one declared payload and return publishable artifacts."""
    with ctx.activity("构建 FPK", target=payload.artifact_name):
        return _package(ctx, payload, fnpack)


def _package(ctx, payload, fnpack):
    if platform.system() != "Linux":
        raise ValueError("FPK packaging requires native Linux")
    validate_payload(payload, ctx.project.data["app_id"])
    selected = ctx.project.data.get("version")
    if selected is not None and payload.values["VERSION"] != selected:
        raise ValueError("payload VERSION must equal the selected project.version")
    package_dir = ctx.work / "fpk" / payload.artifact_name.removesuffix(".fpk")
    stage = package_dir / "stage"
    with ctx.activity("准备 FPK 图标"):
        payload, icon_metadata = prepare_payload_icons(ctx.project, payload, ctx.work)
    expected = create_stage(payload, stage, activity=ctx.activity,
                            capabilities=ctx.project.data.get("fnpackkit", {}).get("capabilities", {}))
    with ctx.activity("识别 fnpack 工具"):
        version_output = ctx.runner.capture([str(fnpack), "--help"], cwd=stage)
    match = re.search(r"(?m)^Version\s+(\S+)", version_output)
    if not match:
        raise ValueError("cannot identify fnpack version")
    with ctx.activity("执行 fnpack 封包"):
        # fnpack copies directory modes through a temporary tree subject to umask.
        ctx.runner.run([sys.executable, "-c", _FNPACK_EXEC, str(fnpack), "build"], cwd=stage)
    built = stage / (payload.values["APP_ID"] + ".fpk")
    if built.is_symlink() or not built.is_file():
        raise ValueError("fnpack produced no regular FPK; inspect its diagnostic")
    with ctx.activity("验证实际 FPK 归档"):
        actual = verify_package(built, stage, expected)
    artifact = package_dir / payload.artifact_name
    built.replace(artifact)
    with ctx.activity("计算 FPK SHA-256"):
        checksum = digest(artifact)
    checksum_file = artifact.with_suffix(".fpk.sha256")
    checksum_file.write_text(f"{checksum}  {artifact.name}\n")
    report_file = artifact.with_suffix(".build.json")
    provenance = {"schema": 1, "app_id": ctx.project.data["app_id"],
                  "version": payload.values["VERSION"], "platform": payload.values["PLATFORM"],
                  "fnpackkit_version": tool_version(), "fnpack_version": match.group(1),
                  "fpk_sha256": checksum, "files": actual,
                  "build": ctx.metadata, "application": payload.metadata}
    if icon_metadata is not None:
        provenance["icons"] = icon_metadata
    report_file.write_text(json.dumps(provenance, ensure_ascii=False, indent=2) + "\n")
    ctx.metadata.setdefault("fpk_reports", {})[artifact.name] = str(report_file)
    return [Artifact(path, path.name) for path in (artifact, checksum_file)]
