"""Load only explicitly selected, trusted application build adapters."""
import importlib.util
from pathlib import Path
import sys

from .archive import safe_relative


def load_plan(project, declaration):
    filename, separator, function = declaration.partition(":")
    if not separator or not function.isidentifier():
        raise ValueError("adapter must use relative/file.py:function syntax")
    source = project.root / safe_relative(filename)
    if source.is_symlink() or not source.is_file() or not source.resolve().is_relative_to(project.root):
        raise ValueError("adapter must be a regular Python file inside the selected project")
    name = "_fnpackkit_application_adapter"
    spec = importlib.util.spec_from_file_location(name, source)
    if spec is None or spec.loader is None:
        raise ValueError("cannot load application adapter")
    module = importlib.util.module_from_spec(spec)
    previous = sys.modules.get(name)
    sys.modules[name] = module
    sys.path.insert(0, str(source.parent))
    try:
        spec.loader.exec_module(module)
        entry = getattr(module, function)
        result = entry(project)
    finally:
        sys.path.remove(str(source.parent))
        if previous is None:
            sys.modules.pop(name, None)
        else:
            sys.modules[name] = previous
    if not isinstance(result, dict) or set(result) - {"steps", "payload", "metadata", "verify", "artifacts"}:
        raise ValueError("adapter plan accepts steps, payload, metadata, verify and artifacts")
    if "steps" not in result or "payload" not in result:
        raise ValueError("adapter plan requires steps and payload")
    return result
