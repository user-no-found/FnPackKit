"""Opt-in shared uninstall policy, wizard and post-payload cleanup."""
import json
from pathlib import Path
import re

from .contracts import UninstallPolicy
from .lifecycle import FUNCTIONS

ROOTS = {'etc', 'var', 'home', 'meta', 'tmp'}


def parse_uninstall(data):
    if data is None:
        return None
    fields = {'protected', 'transient', 'title', 'label', 'retain_label', 'delete_label'}
    if not isinstance(data, dict) or set(data) - fields - {'nas_user_roots', 'state_owner'} or not fields <= set(data):
        raise ValueError('uninstall requires protected/transient paths and wizard labels')
    for key in ('protected', 'transient'):
        if not isinstance(data[key], list):
            raise ValueError(f'uninstall.{key} must be an array')
    if 'nas_user_roots' in data and not isinstance(data['nas_user_roots'], (list, tuple)):
        raise ValueError('uninstall.nas_user_roots must be an array')
    policy = UninstallPolicy(**{**data, 'nas_user_roots': tuple(data.get('nas_user_roots', ())), 'protected': tuple(data['protected']),
                               'transient': tuple(data['transient'])})
    validate_uninstall(policy)
    return policy


def _path(value):
    if not isinstance(value, str) or any(c in value for c in '\x00\r\n'):
        raise ValueError('uninstall paths must be literal strings')
    parts = value.split('/')
    if len(parts) < 2 or parts[0] not in ROOTS - {'tmp'}:
        raise ValueError('uninstall path requires etc/var/home/meta and a child')
    if any(p in ('', '.', '..') or ('*' in p and p != '*') or
           any(c in p for c in '?[]\\') for p in parts):
        raise ValueError('uninstall paths allow only a whole-component wildcard')
    return parts


def _overlap(deleted, protected):
    return len(deleted) <= len(protected) and all(
        a == b or a == '*' or b == '*' for a, b in zip(deleted, protected))


def validate_uninstall(policy):
    if policy is None:
        return
    if not isinstance(policy, UninstallPolicy):
        raise ValueError('uninstall must be an UninstallPolicy declaration')
    if policy.state_owner not in ('package', 'root'):
        raise ValueError('uninstall.state_owner must be package or root')
    if policy.state_owner == 'root' and policy.nas_user_roots:
        raise ValueError('root state ownership cannot include NAS-user roots')
    if (not isinstance(policy.nas_user_roots, tuple) or len(set(policy.nas_user_roots)) != len(policy.nas_user_roots)
            or any(value not in ('var', 'tmp') for value in policy.nas_user_roots)):
        raise ValueError('nas_user_roots must be a unique tuple of var/tmp')
    for field in ('protected', 'transient'):
        values = getattr(policy, field)
        if not isinstance(values, tuple) or len(values) != len(set(values)):
            raise ValueError('uninstall path declarations must be unique tuples')
        for value in values:
            _path(value)
    if any(value.split('/')[0] in policy.nas_user_roots for value in policy.transient):
        raise ValueError('NAS-user roots cannot declare package-account transient traversal; tmp is removed as a whole')
    for field in ('title', 'label', 'retain_label', 'delete_label'):
        value = getattr(policy, field)
        if not isinstance(value, str) or not value.strip() or '\x00' in value:
            raise ValueError('uninstall wizard labels must be nonempty text')
    if any(_overlap(_path(t), _path(p)) for t in policy.transient for p in policy.protected):
        raise ValueError('transient uninstall path overlaps protected data')


GUARD_MARKER = '# fnpackkit:uninstall-guard'
PHASE_HOOKS = (
    ('uninstall_init', 'preflight', 'uninstall_init'),
    ('uninstall_callback', 'cleanup', 'uninstall_callback'),
    ('install_callback', 'prepare', 'install_callback'),
    ('upgrade_callback', 'prepare', 'upgrade_callback'),
    ('upgrade_init', 'prepare', 'upgrade_init'),
    ('config_init', 'prepare', 'config_init'),
    ('config_callback', 'prepare', 'config_callback'),
)


def _guard(action, label):
    return (GUARD_MARKER + '\n'
            'fnp_package=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)\n'
            'if fnp_cleanup_error=$(/usr/bin/python3 -I "$fnp_package/cmd/hooks/fnpack_uninstall.py" '
            + action + ' 2>&1); then :; else\n'
            '    fnp_fail 1 "' + label + ': $fnp_cleanup_error"\nfi\n')


def _insert_guard(path, guard, *, main=False):
    source = path.read_text(encoding='utf-8')
    if GUARD_MARKER in source or source.count(FUNCTIONS) != 1:
        raise ValueError(f'generated lifecycle command cannot accept shared uninstall guard: {path.name}')
    head, separator, tail = source.partition(FUNCTIONS)
    if main:
        guard = 'if [ "${1:-}" = start ]; then\n' + guard + 'fi\n'
    path.write_text(head + separator + guard + tail, encoding='utf-8')


def generate_uninstall(stage, policy, app_id, lifecycle):
    """Generate callbacks owned by the toolkit; app stop runs after preflight."""
    if policy is None:
        return
    validate_uninstall(policy)
    if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9._-]*', app_id):
        raise ValueError('uninstall requires a safe app identifier')
    if lifecycle is None or 'uninstall_callback' in lifecycle.hooks:
        raise ValueError('shared uninstall requires lifecycle without a custom post callback')
    privilege = json.loads((stage / 'config/privilege').read_text(encoding='utf-8'))
    if privilege.get('defaults', {}).get('run-as') != 'root':
        raise ValueError('complete shared uninstall requires root lifecycle privilege')
    required_identity = 'current' if policy.nas_user_roots or policy.state_owner == 'root' else 'package'
    if any(command.run_as != required_identity for command in lifecycle.hooks.values()):
        raise ValueError('shared uninstall hooks do not match their declared state ownership')
    wizard = stage / 'wizard/uninstall'
    hooks = stage / 'cmd/hooks'
    helper = hooks / 'fnpack_uninstall.py'
    support = hooks / 'fnpack_uninstall_support.py'
    settings = hooks / 'fnpack_uninstall_policy.json'
    if wizard.exists() or helper.exists() or support.exists() or settings.exists():
        raise ValueError('shared uninstall would overwrite custom files')
    for phase, _, _ in PHASE_HOOKS:
        if not (stage / 'cmd' / phase).is_file():
            raise ValueError(f'shared uninstall requires generated lifecycle command: {phase}')
    if not (stage / 'cmd/main').is_file():
        raise ValueError('shared uninstall requires generated main command')
    wizard.parent.mkdir(exist_ok=True)
    wizard.write_text(json.dumps([{'stepTitle': policy.title, 'items': [{
        'type': 'radio', 'field': 'wizard_data_action', 'label': policy.label,
        'initValue': 'retain', 'options': [
            {'label': policy.retain_label, 'value': 'retain'},
            {'label': policy.delete_label, 'value': 'delete'}]}]}], ensure_ascii=False) + '\n',
        encoding='utf-8')
    hooks.mkdir(parents=True, exist_ok=True)
    data = {'app_id': app_id, 'transient': list(policy.transient),
            'nas_user_roots': list(policy.nas_user_roots), 'state_owner': policy.state_owner}
    settings.write_text(json.dumps(data, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    helper.write_text(Path(__file__).with_name('uninstall_runtime.py').read_text(encoding='utf-8'),
                      encoding='utf-8')
    support.write_text(Path(__file__).with_name('fnpack_uninstall_support.py').read_text(encoding='utf-8'),
                       encoding='utf-8')
    helper.chmod(0o755)
    for phase, action, label in PHASE_HOOKS:
        _insert_guard(stage / 'cmd' / phase, _guard(action, label))
    _insert_guard(stage / 'cmd/main', _guard('prepare', 'main:start'), main=True)
