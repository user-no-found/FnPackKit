#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
case "$#" in
    0) ;;
    2)
        if [ "$1" != "--buildflow" ] || [ -z "$2" ]; then
            printf 'Usage: %s [--buildflow /path/to/BuildFlow]\n' "$0" >&2
            exit 2
        fi
        BUILDFLOW_PATH=$2
        export BUILDFLOW_PATH
        ;;
    *)
        printf 'Usage: %s [--buildflow /path/to/BuildFlow]\n' "$0" >&2
        exit 2
        ;;
esac
config_root=${XDG_CONFIG_HOME:-"$HOME/.config"}
if [ -f "$config_root/buildflow/environment.sh" ]; then
    . "$config_root/buildflow/environment.sh"
fi
if [ -z "${BUILDFLOW_PATH:-}" ] || [ ! -f "$BUILDFLOW_PATH/src/buildflow/__init__.py" ]; then
    printf 'BuildFlow is required. Run /path/to/BuildFlow/setup.sh first, or pass --buildflow /path/to/BuildFlow.\n' >&2
    exit 1
fi
export PYTHONPATH="$BUILDFLOW_PATH/src${PYTHONPATH:+:$PYTHONPATH}"
exec "${PYTHON:-python3}" -m buildflow.bootstrap --register BUILDFLOW_PATH "$BUILDFLOW_PATH" --register FNPACKKIT_PATH "$root"
