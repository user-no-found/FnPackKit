"""Apply FPK schema validation to BuildFlow's bounded read-only discovery."""
from buildflow.discovery import discover as discover_projects
from buildflow.metadata import version as engine_version
from buildflow.project import load_project
from .manifest import validate_project


def load_fpk_project(path):
    project = load_project(path, engine_version())
    validate_project(project)
    return project


def discover(roots, **budgets):
    result = discover_projects(roots, validator=load_fpk_project, **budgets)
    result.candidates[:] = [candidate for candidate in result.candidates
                            if "fpk" in candidate.targets or not candidate.targets and candidate.error]
    return result
