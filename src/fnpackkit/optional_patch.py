"""Restrict optional patches to explicit regular-file Git text diffs."""
import re

from .optional_contracts import relative


def _section(current, headers, in_hunk, text_modify, shared):
    if current is None:
        return
    if headers != {"---", "+++"} or not in_hunk:
        raise ValueError("optional patch requires ordinary unified text hunks")
    if current in shared and not text_modify:
        raise ValueError(f"optional shared patch target requires text modification without mode changes: {current}")


def patch_plan(content, *, shared_targets=()):
    """Return safe paths/modes; declared shared paths permit only ordinary text modifies."""
    try:
        text = content.decode("utf-8")
    except UnicodeDecodeError as exc:
        raise ValueError("optional patches must be UTF-8 Git text diffs") from exc
    if "\0" in text:
        raise ValueError("optional patches must not contain binary NUL data")
    shared = set(shared_targets)
    paths = {}
    current = None
    headers = set()
    in_hunk, text_modify = False, True
    for line in text.splitlines():
        if line.startswith("diff --git "):
            _section(current, headers, in_hunk, text_modify, shared)
            match = re.fullmatch(r"diff --git a/(\S+) b/(\S+)", line)
            if not match or match[1] != match[2]:
                raise ValueError("optional patch renames/quoted paths are not supported")
            current = relative(match[1])
            if current in paths:
                raise ValueError(f"duplicate optional patch target: {current}")
            paths[current] = None
            headers, in_hunk, text_modify = set(), False, True
        elif current is None:
            if line:
                raise ValueError("optional patch must start with diff --git")
        elif line.startswith("@@ "):
            if headers != {"---", "+++"}:
                raise ValueError("optional patch hunk requires matching file headers")
            in_hunk = True
        elif not in_hunk:
            if line.startswith(("--- ", "+++ ")):
                prefix = line[:3]
                expected = ("a/" if prefix == "---" else "b/") + current
                if line[4:] not in {expected, "/dev/null"} or prefix in headers:
                    raise ValueError("optional patch file header/path mismatch")
                headers.add(prefix)
                if line[4:] == "/dev/null":
                    text_modify = False
                    if prefix == "---" and paths[current] is None:
                        paths[current] = 0o644
            elif line.startswith("index "):
                if not re.fullmatch(r"index [0-9a-f]+\.\.[0-9a-f]+(?: 100(?:644|755))?", line):
                    raise ValueError("optional patch index mode must describe a regular file")
            elif line.startswith(("new file mode ", "deleted file mode ", "old mode ", "new mode ")):
                text_modify = False
                mode = line.rsplit(" ", 1)[-1]
                if mode not in {"100644", "100755"}:
                    raise ValueError("optional patch symlink/submodule/special mode is forbidden")
                if line.startswith(("new file mode ", "new mode ")):
                    paths[current] = int(mode[-3:], 8)
            elif line:
                raise ValueError(f"unsupported optional patch header: {line[:80]}")
        elif line and line[0] not in " +-\\":
            raise ValueError("optional patch contains unsupported non-text data")
    _section(current, headers, in_hunk, text_modify, shared)
    if not paths:
        raise ValueError("optional patch requires ordinary unified text hunks")
    return paths


def patch_paths(content):
    """Return every validated target for consumers that only need path safety."""
    return list(patch_plan(content))
