#!/usr/bin/python3
"""Standalone, read-only Docker/Compose/runtime probe shipped in an FPK."""
import argparse
import json
import os
import re
import subprocess
import sys


def check(binary="/usr/bin/docker", runtime=""):
    if not os.path.isabs(binary) or os.path.normpath(binary) != binary:
        raise ValueError("Docker executable must be an absolute canonical path")
    if runtime and not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,127}", runtime):
        raise ValueError("Invalid Docker runtime name")
    for arguments, label in ((["compose", "version"], "Docker Compose"),
                             (["info", "--format", "{{json .Runtimes}}"], "Docker daemon")):
        result = subprocess.run([binary, *arguments], capture_output=True, timeout=15,
                                env={"PATH": "/usr/bin:/usr/sbin:/bin:/sbin", "LANG": "C"})
        if result.returncode:
            raise RuntimeError(f"{label} is unavailable; enable Docker in fnOS")
    runtimes = json.loads(result.stdout)
    if not isinstance(runtimes, dict):
        raise ValueError("Invalid Docker runtime information")
    if runtime and runtime not in runtimes:
        raise RuntimeError(f"Docker runtime {runtime} is unavailable; configure its host container support")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--runtime", default="")
    options = parser.parse_args()
    try:
        check(runtime=options.runtime)
    except (OSError, ValueError, RuntimeError, subprocess.TimeoutExpired) as error:
        message = f"Docker prerequisite: {error}\n"
        print(message, file=sys.stderr, end="")
        if path := os.environ.get("TRIM_TEMP_LOGFILE"):
            with open(path, "a", encoding="utf-8") as log:
                log.write(message)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
