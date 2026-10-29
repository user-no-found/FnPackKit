"""Compose application build steps and FPK operations in one BuildFlow pipeline."""
from pathlib import Path

from buildflow import api as engine
from buildflow.contracts import Artifact, Step
from buildflow.metadata import version as engine_version
from buildflow.project import load_project
from .adapter import load_plan
from .archive import safe_relative
from .contracts import Payload
from .installed import installed_version
from .manifest import read_payload, validate_project
from .packaging import package
from .staging import digest


def build(project_path, *, steps=None, payload=None, verify=None, artifacts=None,
          fnpack="fnpack", metadata=None, ui=None, build_version=None,
          next_version=False, channel=None):
    """Seed from installation, then select under lock before loading the adapter."""
    validate_project(load_project(project_path, engine_version()))

    def configure(project):
        return _configuration(project, steps, payload, verify, artifacts, fnpack, metadata)

    return engine.build(project_path, configure=configure, ui=ui,
                        build_version=build_version, next_version=next_version,
                        channel=channel, installed_version=installed_version)


def _configuration(project, steps, payload, verify, reports, fnpack, metadata):
    """Capture only the freshly reloaded project after BuildFlow's locked selection."""
    config = validate_project(project)
    if steps is None and "adapter" in config:
        plan = load_plan(project, config["adapter"])
        steps = plan["steps"]
        payload = plan["payload"] if payload is None else payload
        metadata = {**plan.get("metadata", {}), **(metadata or {})}
        verify = plan.get("verify") if verify is None else verify
        reports = plan.get("artifacts") if reports is None else reports
    if steps is None:
        steps = engine.declared_steps(project)
    for name, callback in (("verify", verify), ("artifacts", reports)):
        if callback is not None and not callable(callback):
            raise ValueError(f"{name} must be a callable")
    package_artifacts = []

    def package_payloads(ctx):
        declarations = payload(ctx) if callable(payload) else payload
        if declarations is None:
            value = config["payload"].replace("{work}", str(ctx.work)).replace("{project}", str(project.root))
            declarations = Path(value)
        if isinstance(declarations, (str, Path)):
            declarations = read_payload(declarations)
        if isinstance(declarations, Payload):
            declarations = [declarations]
        if not isinstance(declarations, list) or not declarations:
            raise ValueError("payload must return one or more Payload declarations")
        names = [item.artifact_name for item in declarations]
        if len(names) != len(set(names)):
            raise ValueError("payload artifact names must be unique")
        for item in declarations:
            package_artifacts.extend(package(ctx, item, fnpack))

    def verify_packages(ctx):
        result = verify(ctx, tuple(package_artifacts))
        if result is not None:
            raise ValueError("verify must return None on success or raise on failure")

    def publication_artifacts(ctx):
        extra = reports(ctx) if reports is not None else []
        if not isinstance(extra, list) or not all(isinstance(item, Artifact) for item in extra):
            raise ValueError("artifacts callback must return a list of Artifact report declarations")
        records = []
        names = set()
        for report in extra:
            name = safe_relative(report.name)
            path = Path(report.path)
            if name in names or path.is_symlink() or not path.is_file() or not path.resolve().is_relative_to(ctx.work.resolve()):
                raise ValueError("application reports must be unique regular files inside this build workspace")
            names.add(name)
            records.append({"name": name, "path": str(path.resolve()), "sha256": digest(path)})
        if records:
            ctx.metadata["application_reports"] = records
        return package_artifacts

    pipeline = [*steps, Step("FPK 渲染、封包与实际归档验证", package_payloads)]
    if verify is not None:
        pipeline.append(Step("应用实际FPK验证", verify_packages))
    return {"steps": pipeline, "artifacts": publication_artifacts, "metadata": metadata or {}}
