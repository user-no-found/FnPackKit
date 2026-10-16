"""Generate explicitly declared fnOS entrypoints and shared error diagnostics."""
from pathlib import Path
import re
import shlex

from .archive import safe_relative
from .contracts import Lifecycle, LifecycleCommand

PHASES = ('install_init', 'install_callback', 'upgrade_init', 'upgrade_callback',
          'uninstall_init', 'uninstall_callback', 'config_init', 'config_callback')
ACTIONS = ('start', 'stop', 'status')
KEYS = {*PHASES, *(f'main:{action}' for action in ACTIONS)}
WITHOUT_TARGET = {'install_init', 'uninstall_callback'}

FUNCTIONS = """# Shared fnOS diagnostics; values and command arguments are not logged.
fnp_fail() {
    fnp_code=$1
    shift
    printf '%s\\n' "$*" >&2
    if [ -n "${TRIM_TEMP_LOGFILE:-}" ]; then
        if ! printf '%s\\n' "$*" >> "$TRIM_TEMP_LOGFILE"; then
            printf '%s\\n' 'Cannot write lifecycle error log.' >&2
        fi
    fi
    exit "$fnp_code"
}
"""


def parse_lifecycle(data):
    """Read the optional payload lifecycle declaration without legacy aliases."""
    if data is None:
        return None
    if not isinstance(data, dict) or set(data) != {'hooks'} or not isinstance(data['hooks'], dict):
        raise ValueError('lifecycle requires a hooks mapping')
    hooks = {}
    for phase, value in data['hooks'].items():
        if not isinstance(value, dict) or set(value) - {'location', 'path', 'args', 'required_env', 'run_as'}:
            raise ValueError('invalid lifecycle command declaration')
        for key in ('args', 'required_env'):
            if key in value and not isinstance(value[key], list):
                raise ValueError(f'lifecycle {key} must be an array')
        hooks[phase] = LifecycleCommand(value.get('location'), value.get('path'),
                                        tuple(value.get('args', ())), tuple(value.get('required_env', ())),
                                        value.get('run_as', 'current'))
    result = Lifecycle(hooks)
    validate_lifecycle(result)
    return result


def validate_lifecycle(profile, stage=None):
    if profile is None:
        return
    if not isinstance(profile, Lifecycle) or not isinstance(profile.hooks, dict):
        raise ValueError('lifecycle must be a Lifecycle declaration')
    for phase, command in profile.hooks.items():
        if phase not in KEYS or not isinstance(command, LifecycleCommand):
            raise ValueError(f'unknown lifecycle phase or command: {phase}')
        if command.run_as not in ('current', 'package'):
            raise ValueError('lifecycle run_as must be current or package')
        if command.location not in ('target', 'package'):
            raise ValueError('lifecycle location must be target or package')
        if not isinstance(command.path, str):
            raise ValueError('lifecycle command path must be a relative string')
        path = safe_relative(command.path)
        if command.location == 'target' and phase in WITHOUT_TARGET:
            raise ValueError(f'{phase} cannot depend on target before deployment or after cleanup')
        if command.location == 'package' and not path.startswith('cmd/hooks/'):
            raise ValueError('package lifecycle commands must be under cmd/hooks/')
        if not isinstance(command.args, tuple) or any(
                not isinstance(arg, str) or any(char in arg for char in '\0\r\n')
                for arg in command.args):
            raise ValueError('lifecycle args must be literal strings')
        if not isinstance(command.required_env, tuple) or any(
                not isinstance(name, str) or not re.fullmatch(r'TRIM_[A-Z0-9_]+', name)
                for name in command.required_env):
            raise ValueError('lifecycle required_env must contain TRIM_ variable names')
        if stage is not None:
            file = stage / ('app' if command.location == 'target' else '') / path
            if file.is_symlink() or not file.is_file() or not file.stat().st_mode & 0o111:
                raise ValueError(f'lifecycle command must be a regular executable: {path}')


def _body(phase, command):
    prefix = f'fnp_phase={shlex.quote(phase)}\n'
    if command is None:
        return prefix + ('exit 3\n' if phase == 'main:status' else 'exit 0\n')
    required = set(command.required_env)
    if command.location == 'target':
        required.add('TRIM_APPDEST')
    lines = [prefix]
    for name in sorted(required):
        lines.append(f'[ -n "${{{name}:-}}" ] || fnp_fail 1 "$fnp_phase: missing required {name}"\n')
    if command.location == 'target':
        lines.append('fnp_base=$TRIM_APPDEST\n')
    else:
        lines.append('fnp_base=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P) || '
                     'fnp_fail 1 "$fnp_phase: package directory unavailable"\n')
    lines.append(f'fnp_program="$fnp_base"/{shlex.quote(command.path)}\n')
    lines.append('[ -f "$fnp_program" ] && [ -x "$fnp_program" ] || '
                 'fnp_fail 1 "$fnp_phase: required command missing or not executable"\n')
    args = ' '.join(shlex.quote(arg) for arg in command.args)
    invoke = '"$fnp_program"'
    if command.run_as == 'package':
        lines.append('fnp_package=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)\n')
        invoke = '/usr/bin/python3 -I "$fnp_package/cmd/hooks/fnpack_identity.py" "$fnp_program"'
    lines.append(f'if {invoke} {args}; then exit 0; else fnp_result=$?; fi\n')
    if phase == 'main:status':
        lines.append('[ "$fnp_result" != 3 ] || exit 3\n')
    lines.append('fnp_fail "$fnp_result" "$fnp_phase: command failed (exit $fnp_result)"\n')
    return ''.join(lines)


def generate_lifecycle(stage, profile):
    """Opt in to generated entries; custom scripts without a profile remain untouched."""
    if profile is None:
        return
    validate_lifecycle(profile, stage)
    command_dir = stage / 'cmd'
    generated = (*PHASES, 'main', 'fnpackkit.sh')
    if any((command_dir / name).exists() for name in generated):
        raise ValueError('generated lifecycle would overwrite custom cmd scripts')
    command_dir.mkdir(exist_ok=True)
    if any(command.run_as == 'package' for command in profile.hooks.values()):
        helper = command_dir / 'hooks/fnpack_identity.py'
        if helper.exists():
            raise ValueError('generated lifecycle would overwrite identity helper')
        helper.parent.mkdir(exist_ok=True)
        helper.write_text(Path(__file__).with_name('lifecycle_identity.py').read_text())
        helper.chmod(0o755)
    header = '#!/bin/sh\nset -eu\n' + FUNCTIONS
    for phase in PHASES:
        target = command_dir / phase
        target.write_text(header + _body(phase, profile.hooks.get(phase)))
        target.chmod(0o755)
    body = 'case "${1:-}" in\n'
    for action in ACTIONS:
        phase = 'main:' + action
        body += f'{action})\n' + _body(phase, profile.hooks.get(phase)) + ';;\n'
    body += '*) fnp_fail 1 "main: expected start, stop or status" ;;\nesac\n'
    (command_dir / 'main').write_text(header + body)
    (command_dir / 'main').chmod(0o755)
    (command_dir / 'fnpackkit.sh').write_text(FUNCTIONS)
    (command_dir / 'fnpackkit.sh').chmod(0o644)
