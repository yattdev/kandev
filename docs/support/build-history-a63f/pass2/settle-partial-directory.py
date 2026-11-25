#!/usr/bin/env python3
"""Settle only this migration's newly created empty metadata directory."""
import json
from pathlib import Path
import subprocess

SCRIPT = r'''
import hashlib,json,os,stat
from pathlib import Path
h=lambda s:hashlib.sha256(s.encode()).hexdigest()
r='/data/tasks/8355-impl-menter-le_vmaz2aad';project='kd_8355_connector346'
base=Path('/data/agent-docker-models');task=base/h(r);partial=task/'successful-builds'
parent=task.stat(follow_symlinks=False)
assert stat.S_ISDIR(parent.st_mode) and parent.st_uid==1000 and parent.st_mode&0o777==0o700
claim=base/'projects'/(h(project)+'.json');model=task/claim.name
before=[hashlib.sha256(p.read_bytes()).hexdigest() for p in (claim,model)]
assert not (partial/h(r)/(h(project+'_php-test:dev365-b07-c054')+'.json')).exists()
fd=os.open(partial,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)
try:
 s=os.fstat(fd)
 assert s.st_uid==0 and s.st_mode&0o777==0o700 and s.st_nlink==2 and not os.listdir(fd)
 assert (s.st_dev,s.st_ino)==(partial.stat(follow_symlinks=False).st_dev,partial.stat(follow_symlinks=False).st_ino)
 os.fchown(fd,parent.st_uid,parent.st_gid);os.fsync(fd)
 after=os.fstat(fd)
 assert (after.st_uid,after.st_gid)==(parent.st_uid,parent.st_gid)
 assert [hashlib.sha256(p.read_bytes()).hexdigest() for p in (claim,model)]==before
 print(json.dumps(dict(scope='only migration-created empty successful-builds directory',device=s.st_dev,inode=s.st_ino,previous_uid=s.st_uid,uid=after.st_uid,gid=after.st_gid,mode=oct(after.st_mode&0o777),claim_and_model_unchanged=True,receipt_created=False)))
finally:os.close(fd)
'''
result=subprocess.run(['docker','exec','-i','--user','0','kandev','/usr/bin/python3','-I','-B','-c',SCRIPT],capture_output=True,check=True)
output=Path(__file__).with_name('partial-directory-settlement.json')
output.write_bytes(result.stdout)
print(result.stdout.decode())
