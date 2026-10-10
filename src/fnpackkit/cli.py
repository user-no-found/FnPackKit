"""Native FPK CLI with explicit selection and no stored project registry."""
import argparse
import json
from pathlib import Path
import sys

from buildflow.cli import select_project
from buildflow.contracts import BuildError
from .api import build
from .discovery import discover, load_fpk_project
from .metadata import tool_version


def main(argv=None):
    parser = argparse.ArgumentParser(description="FnPackKit 原生飞牛应用构建")
    parser.add_argument("--version", action="version", version=tool_version())
    selection = parser.add_mutually_exclusive_group()
    selection.add_argument("--project", type=Path)
    selection.add_argument("--workspace", type=Path, action="append", default=[])
    parser.add_argument("--discover", action="store_true", help="只读发现，不执行应用代码")
    parser.add_argument("--max-depth", type=int, default=4)
    parser.add_argument("--max-directories", type=int, default=1000)
    parser.add_argument("--fnpack", default="fnpack", help="fnpack executable path")
    versions = parser.add_mutually_exclusive_group()
    versions.add_argument("--build-version", help="选择并写回 project.json 的构建版本")
    versions.add_argument("--next-version", action="store_true", help="使用所选发布类型的下一版本")
    parser.add_argument("--channel", choices=("stable", "test"), help="正式版或测试版")
    args = parser.parse_args(argv)
    try:
        if args.discover:
            if not args.workspace:
                raise ValueError("--discover requires --workspace")
            result = discover(args.workspace, max_depth=args.max_depth, max_directories=args.max_directories)
            print(json.dumps({"candidates": [{**vars(item), "path": str(item.path)} for item in result.candidates],
                              "warnings": result.warnings}, ensure_ascii=False, indent=2))
            return 2 if result.warnings else 0
        project = select_project(args.project, args.workspace, target="fpk", max_depth=args.max_depth,
                                 max_directories=args.max_directories, validator=load_fpk_project)
        result = build(project.path, fnpack=args.fnpack, build_version=args.build_version,
                       next_version=args.next_version, channel=args.channel)
        print(result.output)
        return 0
    except BuildError as exc:
        if not exc.reported:
            print(f"FnPackKit: {exc}", file=sys.stderr)
        return exc.exit_code
    except (OSError, ValueError, ImportError, AttributeError, KeyError, TypeError) as exc:
        print(f"FnPackKit: {exc}", file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        return 130
