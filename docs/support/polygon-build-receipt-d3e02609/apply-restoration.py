#!/usr/bin/env python3
"""Reviewed host launcher, not a task-facing command or installed capability."""
import hashlib
import json
from pathlib import Path
import subprocess

HERE = Path(__file__).resolve().parent
review = json.loads((HERE/'reviewed.json').read_bytes())
source = (HERE/'restore-known-output.py').read_bytes()
proof = (HERE/'native-output-proof.json').read_bytes()
assert hashlib.sha256(source).hexdigest() == review['restore_sha256']
assert hashlib.sha256(proof).hexdigest() == review['proof_sha256']
repo = HERE.parents[1]
assert subprocess.run(['git','branch','--show-current'],cwd=repo,capture_output=True,text=True,check=True).stdout.strip() == 'main'
def identity():
    return subprocess.run(['docker','inspect','kandev','--format','{{.Id}} {{.Image}} {{.State.StartedAt}} {{.RestartCount}}'],capture_output=True,text=True,check=True).stdout.strip()
before = identity()
result = subprocess.run(['docker','exec','-i','--user','0',before.split()[0],'/usr/bin/python3','-I','-B','-c',source.decode(),'--apply'],input=json.dumps(review).encode()+b'\n'+proof,capture_output=True)
(HERE/'restoration-attempt.json').write_text(json.dumps(dict(returncode=result.returncode,stdout=result.stdout.decode(),stderr=result.stderr.decode()),indent=2)+'\n')
if result.returncode:
    raise SystemExit(result.stderr.decode())
receipt=json.loads(result.stdout)
after=identity(); receipt.update(container_identity_before=before,container_identity_after=after,unchanged_container=before==after,restart=False)
(HERE/'restoration.json').write_text(json.dumps(receipt,indent=2)+'\n')
assert before==after
print(json.dumps(receipt,indent=2))
