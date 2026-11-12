"""Select, freeze and validate declarative optional packages before production steps."""
import copy
import sys

from buildflow.console import prompt_line
from buildflow.contracts import BuildError
from .optional_contracts import ID, enabled, json_bytes
from .optional_manifest import freeze, scan
from .optional_source import apply_source
from .optional_snapshot import fingerprint, validate_selection

__all__ = ["selection", "validate_selection", "apply_source"]


def selection(ctx):
    """Return a detached JSON-compatible record from pre-build optional preparation."""
    record = ctx.metadata.get("optional_packages")
    if record is None:
        if enabled(ctx.project.data.get("fnpackkit", {})):
            raise ValueError("optional selection has not been prepared")
        return None
    validate_selection(record, runner=ctx.runner)
    return copy.deepcopy(record)


def _requested(features, no_features):
    if type(no_features) is not bool or features is not None and no_features:
        raise ValueError("features and no_features are mutually exclusive")
    if features is None:
        return None
    if isinstance(features, str) or not isinstance(features, (list, tuple)):
        raise ValueError("features must be an explicit sequence of feature ids")
    if any(not isinstance(value, str) or not ID.fullmatch(value) for value in features):
        raise ValueError("features contains an invalid feature id")
    if len(features) != len(set(features)):
        raise ValueError("features contains duplicate feature ids")
    return list(features)


def _choose(ctx, candidates):
    ids = [item["manifest"]["id"] for item in candidates]
    interactive = sys.stdin.isatty() and sys.stderr.isatty()
    with ctx.interaction():
        # Foreground prompts cannot use notices: those are deferred during interaction.
        prompt_line([("可选功能（默认全部安装）：", "1;36")])
        for index, item in enumerate(candidates, 1):
            data = item["manifest"]
            prompt_line([(f"  {index}. [x] {data['label']} ({data['id']})", None)])
        if not interactive:
            return ids
        prompt_line([("选择可选功能 [全部]（编号/id用逗号分隔；0不安装，q取消）: ", "1;36")], end="")
        try:
            answer = input().strip()
        except EOFError as exc:
            raise BuildError("optional packages: 选择已取消", 130) from exc
        if answer.casefold() in {"q", "quit", "cancel"}:
            raise BuildError("optional packages: 选择已取消", 130)
        if answer.casefold() in {"", "all", "a", "全部", "*"}:
            return ids
        if answer.casefold() in {"0", "none", "无", "不安装"}:
            return []
        chosen = []
        for token in answer.replace(" ", ",").split(","):
            if not token:
                continue
            if token.isdecimal() and 1 <= int(token) <= len(ids):
                token = ids[int(token) - 1]
            if token not in ids or token in chosen:
                raise BuildError(f"optional packages: 无效或重复选择 {token!r}")
            chosen.append(token)
        if not chosen:
            raise BuildError("optional packages: 无效选择；使用0明确不安装")
        return chosen


def selection_prepare(project, *, features=None, no_features=False):
    """Select and freeze after version confirmation, before the build banner or steps."""
    requested = _requested(features, no_features)
    if not enabled(project.data.get("fnpackkit", {})):
        if requested is not None or no_features:
            raise ValueError("project has not opted in to fnpackkit.optional_packages")
        return None

    def select(ctx):
        project_name, candidates = scan(
            ctx.project, activity=lambda name: ctx.activity("校验可选功能源", target=name))
        ids = {item["manifest"]["id"] for item in candidates}
        chosen = requested
        if chosen is None:
            chosen = [] if no_features or not candidates else _choose(ctx, candidates)
        missing = set(chosen) - ids
        if missing:
            raise ValueError(f"requested optional feature ids not found: {sorted(missing)}")
        chosen = set(chosen)
        record = {"schema": 1, "project": project_name, "selected": [
            freeze(item, ctx.work) for item in candidates if item["manifest"]["id"] in chosen]}
        record["fingerprint"] = fingerprint(record)
        validate_selection(record)
        snapshot = ctx.work / "optional-packages.json"
        snapshot.write_bytes(json_bytes(record))
        ctx.metadata["optional_packages"] = record
        if candidates and requested is None and not no_features:
            labels = ", ".join(item["label"] for item in record["selected"]) or "不安装任何可选功能"
            prompt_line([("已选可选功能: ", "36"), (labels, "1")])

    return select
