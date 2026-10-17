#!/usr/bin/env python3
"""Compile additive test-only bridges; never overlay an existing source path."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--artifacts', type=Path, required=True)
parser.add_argument('--race', action='store_true')
args = parser.parse_args()
source = Path(__file__).resolve().parent
backend = source.parents[4]
artifacts = args.artifacts.resolve()
artifacts.mkdir(mode=0o700, parents=False, exist_ok=False)
env = dict(os.environ, GOTOOLCHAIN='local', GOPROXY='off', GOSUMDB='off',
           KANDEV_TEST_POSTGRES_DSN='', GOFLAGS='-mod=readonly')
packages = ['internal/task/handlers', 'internal/task/repository/sqlite', 'internal/mcp/server']
names = ['http_bridge.go.txt', 'sqlite_bridge.go.txt', 'composition_test.go.txt']
targets = ['approval_link_composition_bridge.go', 'approval_link_composition_bridge.go',
           'approval_link_composition_overlay_test.go']
replacement = {}
inventory = {}
source_hashes = {}
for package, name, target in zip(packages, names, targets):
    dest = backend/package/target
    if os.path.lexists(dest):
        raise RuntimeError('overlay target must be absent: ' + str(dest))
    body = source/name
    replacement[str(dest)] = str(body)
    info = json.loads(subprocess.check_output(['go', 'list', '-json', './'+package],
                                            cwd=backend, env=env))
    ordinary = info.get('GoFiles', []) + info.get('CgoFiles', [])
    if target in ordinary:
        raise RuntimeError('bridge appears in normal file inventory')
    for filename in ordinary:
        normal_source = backend/package/filename
        source_hashes[str(normal_source)] = hashlib.sha256(normal_source.read_bytes()).hexdigest()
        if 'SyntheticApproval' in normal_source.read_text():
            raise RuntimeError('test-only exports leaked into normal source')
    inventory[package] = ordinary
overlay = artifacts/'overlay.json'
overlay.write_text(json.dumps({'Replace': replacement}, indent=2)+'\n')
(artifacts/'normal-inventory.json').write_text(json.dumps(inventory, indent=2)+'\n')
argv = ['go', 'test', '-overlay='+str(overlay), '-mod=readonly']
if args.race:
    argv.append('-race')
argv += ['./internal/mcp/server', '-run', '^TestApprovalLinkSyntheticComposition$',
         '-count=1', '-v', '-timeout=3m']
with (artifacts/'test.log').open('xb') as log:
    result = subprocess.run(argv, cwd=backend, env=env, stdout=log, stderr=subprocess.STDOUT)
for path, digest in source_hashes.items():
    if hashlib.sha256(Path(path).read_bytes()).hexdigest() != digest:
        raise RuntimeError('source changed during composed test: ' + path)
for path in replacement:
    if os.path.lexists(path):
        raise RuntimeError('overlay materialized a production path: ' + path)
receipt = {'argv': argv, 'cwd': str(backend), 'exit': result.returncode,
           'normal_build_excludes_bridges': True, 'overlay_new_absent_paths_only': True,
           'normal_source_hashes_unchanged': True,
           'execution_head': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=backend, text=True).strip(),
           'production_worker_sha256': hashlib.sha256((backend/'internal/task/handlers/approval_link.go').read_bytes()).hexdigest(),
           'sources_sha256': {p.name: hashlib.sha256(p.read_bytes()).hexdigest()
                              for p in [source/n for n in names]},
           'test_log_sha256': hashlib.sha256((artifacts/'test.log').read_bytes()).hexdigest()}
(artifacts/'receipt.json').write_text(json.dumps(receipt, indent=2)+'\n')
print(json.dumps(receipt, indent=2))
print((artifacts/'test.log').read_text())
raise SystemExit(result.returncode)
