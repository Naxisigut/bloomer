#!/usr/bin/env python3
"""Package a previously built macOS arm64 binary with immutable file checksums."""
import argparse
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import tarfile

root = Path(__file__).resolve().parent.parent
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--binary', type=Path, default=root/'apps/agent/bin/bloomer-agent')
args = parser.parse_args()
version_text = subprocess.check_output([str(args.binary.resolve()), '-version'], text=True).strip()
version = version_text.split()[1]
dist = root/'apps/agent/dist'
package = dist/f'bloomer-agent-{version}-darwin-arm64'
package.mkdir(parents=True, exist_ok=True)
files = {'bloomer-agent': args.binary, 'openapi.yaml': root/'contracts/openapi.yaml', 'INSTALL.md': root/'docs/install.md'}
for name in ['manage.py', 'config.example.json', 'dev.bloomer.agent.plist.template']:
    files[name] = root/'apps/agent/packaging/macos'/name
for name, source in files.items():
    shutil.copy2(source, package/name)
manifest = {'version': version, 'build': version_text, 'platform': 'darwin/arm64', 'sha256': {name: hashlib.sha256((package/name).read_bytes()).hexdigest() for name in sorted(files)}}
(package/'manifest.json').write_text(json.dumps(manifest, indent=2)+'\n')
(package/'SHA256SUMS').write_text(''.join(f'{digest}  {name}\n' for name, digest in manifest['sha256'].items()))
archive = dist/(package.name+'.tar.gz')
with tarfile.open(archive, 'w:gz') as output:
    for path in sorted(package.iterdir()):
        output.add(path, arcname=package.name+'/'+path.name, recursive=False)
print(archive)
