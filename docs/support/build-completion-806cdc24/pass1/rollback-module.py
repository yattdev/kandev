#!/usr/bin/env python3
"""Operator-only hash-gated rollback; never removes receipts or resources."""
import argparse
import hashlib
import json
from pathlib import Path
import runpy
import subprocess

parser=argparse.ArgumentParser()
parser.add_argument('--apply',action='store_true')
args=parser.parse_args()
root=Path(__file__).resolve().parents[3]
folder=Path(__file__).resolve().parent
review=json.loads((folder/'reviewed.json').read_text())
installation=json.loads((folder/'installation.json').read_text())
assert installation['installed_sha256']==review['candidate_sha256']
assert installation['previous_sha256']==review['base_sha256']
backup=Path(installation['backup'])
assert backup.parent.parent==Path('/tmp') and backup.parent.name.startswith('kandev-image-inspect-before-6b30-') and backup.name=='operations.py'
if not args.apply:
    print(json.dumps({'apply':False,'expected_current':review['candidate_sha256'],
                      'restore':review['base_sha256'],'backup':str(backup)}))
    raise SystemExit(0)
branch=subprocess.check_output(['git','branch','--show-current'],cwd=root,text=True).strip()
assert branch=='main'
installer=runpy.run_path(str(root/'scripts/install-task-image-inspect.py'))
docker=installer['docker']
identity=docker('inspect','kandev','--format','{{.Id}} {{.Image}} {{.State.StartedAt}}').stdout.decode().strip()
container=identity.split()[0]
read='''import os,stat,sys
fd=os.open(sys.argv[1],os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK)
with os.fdopen(fd,'rb') as stream:
 before=os.fstat(stream.fileno())
 assert stat.S_ISREG(before.st_mode) and before.st_uid==0 and not before.st_mode&0o077 and before.st_nlink==1
 value=stream.read(1024*1024+1);assert len(value)<=1024*1024
 stamp=lambda s:(s.st_dev,s.st_ino,s.st_size,s.st_mtime_ns,s.st_ctime_ns)
 assert stamp(before)==stamp(os.fstat(stream.fileno()))==stamp(os.stat(sys.argv[1],follow_symlinks=False))
 sys.stdout.buffer.write(value)
'''
source=docker('exec','--user','0',container,'/usr/bin/python3','-I','-c',read,str(backup)).stdout
assert hashlib.sha256(source).hexdigest()==review['base_sha256']
reverse={'request_id':review['request_id'],'base_sha256':review['candidate_sha256'],
         'candidate_sha256':review['base_sha256'],'dependencies':review['dependencies']}
result=docker('exec','-i','--user','0',container,'/usr/bin/python3','-I','-B','-c',installer['INSTALL'],
              input=json.dumps(reverse).encode()+b'\n'+source)
receipt=json.loads(result.stdout)
after=docker('inspect',container,'--format','{{.Id}} {{.Image}} {{.State.StartedAt}}').stdout.decode().strip()
receipt.update(action='rollback',request_id=review['request_id'],container_before=identity,container_after=after,
               unchanged_container=identity==after,receipt_or_resource_changes=False)
(folder/'rollback.json').write_text(json.dumps(receipt,indent=2)+'\n')
assert identity==after
print(json.dumps(receipt))
