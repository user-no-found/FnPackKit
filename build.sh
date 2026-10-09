#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
config_root=${XDG_CONFIG_HOME:-"$HOME/.config"}
if [ -f "$config_root/buildflow/environment.sh" ]; then
    . "$config_root/buildflow/environment.sh"
fi
if [ "${BUILDFLOW_PATH+x}" = x ] && [ ! -f "$BUILDFLOW_PATH/src/buildflow/__init__.py" ]; then
    printf 'Invalid BUILDFLOW_PATH: %s\n' "$BUILDFLOW_PATH" >&2
    exit 1
fi
if [ "${BUILDFLOW_PATH+x}" != x ] && ! "${PYTHON:-python3}" -c 'import buildflow' >/dev/null 2>&1; then
    printf 'BuildFlow is unavailable. Run %s/setup.sh --buildflow /path/to/BuildFlow first.\n' "$root" >&2
    exit 1
fi
export PYTHONPATH="$root/src${BUILDFLOW_PATH:+:$BUILDFLOW_PATH/src}${PYTHONPATH:+:$PYTHONPATH}"
exec "${PYTHON:-python3}" -m fnpackkit "$@"
