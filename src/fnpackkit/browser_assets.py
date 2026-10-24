"""Stage the shared browser bridge and integrity-pinned, dependency-free fnOS SDK."""
import base64
import hashlib
import io
import json
import os
from pathlib import Path
import shutil
import tarfile
from urllib.request import urlopen


MARKER = '<script type="module" src="./fnpackkit/bootstrap.js"></script>'


def _web_directory():
    configured = os.environ.get('FNPACKKIT_PATH')
    if configured is not None and not configured:
        raise ValueError('FNPACKKIT_PATH must select a complete FnPackKit source checkout or extracted sdist')
    root = Path(configured) if configured is not None else Path(__file__).resolve().parents[2]
    required = ('pyproject.toml', 'runtime/go.mod', 'src/fnpackkit/__init__.py',
                'web/sdk.lock.json', 'web/external-links.js')
    try:
        complete = all((root / name).is_file() for name in required)
    except (OSError, ValueError):
        complete = False
    if not complete:
        raise ValueError('FNPACKKIT_PATH must select a complete FnPackKit source checkout or extracted sdist with web resources')
    return root / 'web'


def install_external_links(index, cache, labels):
    """Opt an upstream HTML entry into shared external navigation, before its modules."""
    index, cache = Path(index), Path(cache)
    text = index.read_text()
    if text.count('<head>') != 1 or text.count('</head>') != 1:
        raise ValueError('External navigation requires one HTML head')
    if not isinstance(labels, dict) or any(not isinstance(labels.get(key), str) or not labels[key]
                                         for key in ('title', 'copied', 'copyFailed', 'copy', 'open', 'close')):
        raise ValueError('External navigation labels are required')
    web = _web_directory()
    lock = json.loads((web / 'sdk.lock.json').read_text())
    cache.mkdir(parents=True, exist_ok=True)
    archive = cache / ('trimjs-web-app-' + lock['version'] + '.tgz')
    if not archive.exists():
        with urlopen(lock['url'], timeout=30) as response:
            data = response.read(2 * 1024 * 1024 + 1)
        if len(data) > 2 * 1024 * 1024:
            raise ValueError('fnOS SDK archive exceeds size limit')
    else:
        data = archive.read_bytes()
    actual = 'sha512-' + base64.b64encode(hashlib.sha512(data).digest()).decode()
    if actual != lock['integrity']:
        raise ValueError('fnOS SDK archive integrity mismatch')
    with tarfile.open(fileobj=io.BytesIO(data), mode='r:gz') as package:
        names = package.getnames()
        if len(names) != len(set(names)):
            raise ValueError('Duplicate fnOS SDK archive member')
        members = {}
        for name in ('package/dist/index.js', 'package/package.json', 'package/README.md'):
            member = package.getmember(name)
            if not member.isfile() or member.size > 1024 * 1024:
                raise ValueError('Invalid fnOS SDK archive member')
            members[name] = package.extractfile(member).read()
    metadata = json.loads(members['package/package.json'])
    if (metadata['name'] != lock['package'] or metadata['version'] != lock['version']
            or metadata.get('dependencies')):
        raise ValueError('Unexpected fnOS SDK package identity or dependencies')
    archive.write_bytes(data)
    output = index.parent / 'fnpackkit'
    output.mkdir(exist_ok=True, mode=0o755)
    (output / 'sdk.js').write_bytes(members['package/dist/index.js'])
    (output / 'sdk-package.json').write_bytes(members['package/package.json'])
    (output / 'sdk-README.md').write_bytes(members['package/README.md'])
    shutil.copyfile(web / 'external-links.js', output / 'external-links.js')
    (output / 'bootstrap.js').write_text(
        "import { TrimApp } from './sdk.js';\n"
        "import { createExternalLinks } from './external-links.js';\n"
        "const links = createExternalLinks({ sdk: new TrimApp({ debug: false }), labels: "
        + json.dumps(labels, ensure_ascii=False) + " });\nlinks.install();\n")
    for path in output.iterdir():
        path.chmod(0o644)
    if MARKER not in text:
        index.write_text(text.replace('<head>', '<head><meta name="referrer" content="no-referrer">'
                                     + MARKER))
