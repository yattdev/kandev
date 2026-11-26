#!/usr/bin/env python3
"""Support-only one-output historical migration; no public agent import endpoint.

No engine or owner command is executed. The reviewed native proof supplies the
historical output, while the pre-existing immutable claim supplies exact scope.
"""
import hashlib
import json
import os
from pathlib import Path
import stat
import sys
import tempfile

REQUEST = 'd3e02609-794e-4a8b-b934-11f92bd57817'
ROOT = '/data/tasks/feature-8227-importa_gim9c2mm'
PROJECT = 'kd_8227_pair366'
SERVICE = 'app_base'
TAG = PROJECT + '_' + SERVICE + ':php84-366r10'
REF = 'qzlhr9rvyz88fysq969f52dp2'
# Native completed exporter target, independently read from result descriptors.
# NOT a claimed current engine .Id. The installed reader must still resolve
# the registered tag and require literal .Id equality; mismatch fails closed.
OUTPUT_ID = 'sha256:b341fecdb97d66942f1eb3501343760ffdb1e26e9c77a603eca31354fd3bd137'
CONFIG_ID = 'sha256:0029ce02a40cdab570e49e81d4cd0cf04b57e4806f806b6dbd633b230a6c93f1'
PLATFORM_ID = 'sha256:6093688504ec36bb3b8b33a6c2c7729fa2f3812b1b6b6a1fa978fe3ef7c25d5e'
CLAIM_SHA = '8eeea4229474cd4f4a4a9cb020cd03d4cf0ff8e7b00e12d53d66b2fa36bc509c'
MODEL_SHA = '05aac056cc2f4db7e490c6228cc43cc2502804f3ab1101a298bb736b0f06f8f4'
INDEX_MEDIA = 'application/vnd.oci.image.index.v1+json'
DEPENDENCIES = {
 'kandev-agent-docker-broker': '9514b692d9833fcc64c1978b7553c181c37e84625e645a0ad0521d4b2f298e91',
 'kandev-agent-docker-operations.py': '1f075423e4c89e32caa359e2b27e12086aad4d0adc1f5e4eafe54052a1e7608a',
 'docker': 'd05643a229d78f6cc724b1bc7a1a82904cd7f29ec40a0c61bd105caaff0bbd41',
 'kandev-agent-docker-client': 'fc18ec0fc01645ee8908f026d3e113a729ff815ca008803a1cc4a5d8b3f85133',
 'kandev-agent-guard': '73caa9ebd359e11b1047be861de0ddb2acc456fa166c450407a64af4f840b92f',
}

def sha(data):
    return hashlib.sha256(data).hexdigest()


def read(path, owner, limit=8192):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, 'rb') as stream:
        before = os.fstat(stream.fileno())
        assert stat.S_ISREG(before.st_mode) and before.st_uid == owner
        assert before.st_nlink == 1 and not before.st_mode & 0o022
        data = stream.read(limit + 1)
        stamp = lambda s: (s.st_dev, s.st_ino, s.st_size, s.st_mtime_ns, s.st_ctime_ns)
        assert len(data) <= limit and stamp(before) == stamp(os.fstat(stream.fileno()))
        assert stamp(before) == stamp(path.stat(follow_symlinks=False))
    return data


def validate_proof(proof):
    assert proof['ref'] == REF and proof['event_type'] == 1
    assert proof['error_present'] is False and proof['external_error_present'] is False
    assert proof['created_at'] == '2026-11-25T23:30:27.081324747Z'
    assert proof['completed_at'] == '2026-11-25T23:33:16.030655963Z'
    assert proof['exporters'] == [{'name': 'docker.io/library/' + TAG, 'type': 'image'}]
    assert proof['result_descriptors'] and all(
        d['digest'] == OUTPUT_ID and d['media_type'] == INDEX_MEDIA
        for d in proof['result_descriptors'])
    assert proof['output_manifest_chain'][0]['digest'] == OUTPUT_ID
    assert proof['output_manifest_chain'][0]['mediaType'] == INDEX_MEDIA
    assert len(proof['output_manifest_chain']) == 2
    assert proof['output_manifest_chain'][0]['size'] == 856
    assert proof['output_manifest_chain'][1] == dict(mediaType='application/vnd.oci.image.manifest.v1+json',
        digest=PLATFORM_ID, size=2763, platform=dict(os='linux', architecture='amd64'))
    assert all(d['size'] == 856 for d in proof['result_descriptors'])
    assert proof['verified_output_config_id'] == CONFIG_ID and CONFIG_ID != OUTPUT_ID


def restore(directory, proof_bytes, *, apply=False, enforce_snapshot=False):
    owner = directory.stat(follow_symlinks=False).st_uid
    group = directory.stat(follow_symlinks=False).st_gid
    assert os.getuid() in (0, owner)
    proof = json.loads(proof_bytes)
    validate_proof(proof)
    for path in (directory, directory / 'projects', directory / sha(ROOT.encode())):
        info = path.stat(follow_symlinks=False)
        assert stat.S_ISDIR(info.st_mode) and info.st_uid == owner and not info.st_mode & 0o022
    claim_path = directory / 'projects' / (sha(PROJECT.encode()) + '.json')
    task_directory = directory / sha(ROOT.encode())
    model_path = task_directory / (sha(PROJECT.encode()) + '.json')
    claim_before = read(claim_path, owner)
    model_before = read(model_path, owner, 1024 * 1024)
    if enforce_snapshot:
        assert sha(claim_before) == CLAIM_SHA and sha(model_before) == MODEL_SHA, 'scope snapshot changed; stop'
    expected = dict(task_root=ROOT, cwd=ROOT, project=PROJECT)
    assert json.loads(claim_before) == expected
    model = json.loads(model_before)
    assert model['version'] == 1 and all(model.get(k) == v for k, v in expected.items())
    assert isinstance(model['model'], dict)
    # A model image reference/build key is deliberately not used as output proof.
    output_directory = task_directory / 'successful-builds' / sha(ROOT.encode())
    target = output_directory / (sha(TAG.encode()) + '.json')
    assert not target.exists() and not target.is_symlink(), 'existing receipt preserved; stop'
    record = dict(version=1, kind='successful-build-output', **expected,
                  service=SERVICE, image=TAG, image_id=OUTPUT_ID,
                  observed_at=proof['completed_at'],
                  restoration=dict(request_id=REQUEST, native_ref=REF,
                                   native_proof_sha256=sha(proof_bytes),
                                   index_digest=OUTPUT_ID, platform_manifest_digest=PLATFORM_ID, config_digest=CONFIG_ID,
                                   engine_id_observed=False, image_id_binding='native-completed-output-target',
                                   proof_kind='native-completed-output-and-manifest-chain'))
    payload = (json.dumps(record, sort_keys=True) + '\n').encode()
    assert len(payload) <= 8192
    result = dict(request_id=REQUEST, receipt_sha256=sha(payload),
                  proof_sha256=sha(proof_bytes), claim_sha256=sha(claim_before),
                  saved_model_sha256=sha(model_before), image=TAG, expected_image_id=OUTPUT_ID,
                  engine_id_observed=False,
                  engine_commands=0, owner_commands=0, applied=False)
    if not apply:
        return result
    for path in (directory, task_directory, output_directory.parent, output_directory):
        if not path.exists():
            path.mkdir(mode=0o700)
            if path.stat().st_uid != owner:
                os.chown(path, owner, group, follow_symlinks=False)
        s = path.stat(follow_symlinks=False)
        assert stat.S_ISDIR(s.st_mode) and s.st_uid == owner and not s.st_mode & 0o022
    assert len(list(output_directory.glob('*.json'))) < 64
    assert read(claim_path, owner) == claim_before
    assert read(model_path, owner, 1024 * 1024) == model_before
    fd, temporary = tempfile.mkstemp(prefix='.restored-', dir=output_directory)
    try:
        with os.fdopen(fd, 'wb') as stream:
            if os.fstat(stream.fileno()).st_uid != owner:
                os.fchown(stream.fileno(), owner, group)
            stream.write(payload); stream.flush(); os.fsync(stream.fileno())
        # Atomic no-replace publication; never overwrite any existing receipt.
        os.link(temporary, target, follow_symlinks=False)
    finally:
        os.unlink(temporary)
    fd = os.open(output_directory, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)
    assert read(claim_path, owner) == claim_before
    assert read(model_path, owner, 1024 * 1024) == model_before
    assert read(target, owner) == payload
    result.update(applied=True, claim_and_model_unchanged=True)
    return result


def main():
    assert os.getuid() == 0, 'reviewed host Support only'
    assert sys.argv == [sys.argv[0], '--apply'], 'fixed reviewed apply operation only'
    # Hash review is delivered by the reviewed host worker, never by a task file.
    header = json.loads(sys.stdin.buffer.readline(8192))
    proof_bytes = sys.stdin.buffer.read(8193)
    assert header['request_id'] == REQUEST and len(proof_bytes) <= 8192
    assert sha(proof_bytes) == header['proof_sha256']
    for name, digest in DEPENDENCIES.items():
        assert sha(read(Path('/usr/local/bin') / name, 0, 1024 * 1024)) == digest
    directory = Path('/data/agent-docker-models')
    assert directory.stat(follow_symlinks=False).st_uid == 1000, 'existing broker custody differs'
    print(json.dumps(restore(directory, proof_bytes, apply=True, enforce_snapshot=True)))


if __name__ == '__main__':
    main()
