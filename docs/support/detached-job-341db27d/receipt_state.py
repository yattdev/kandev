"""Pure receipt reconciliation; inputs are one authorized snapshot and own files."""
import re


def reconcile(expected_id, expected_nonce, snapshot, receipt, expected_policy=None):
    if not re.fullmatch(r'[0-9a-f]{64}', expected_id):
        return 'invalid-launch-identity'
    if not snapshot or snapshot.get('id') != expected_id:
        return 'unknown-identity'
    if (snapshot.get('auto_remove') is not False or snapshot.get('restart') not in ('', 'no')
            or any(snapshot.get(key) != value for key, value in (expected_policy or {}).items())):
        return 'policy-mismatch'
    if snapshot.get('running') is True:
        return 'running'
    if snapshot.get('running') is not False or snapshot.get('status') != 'exited':
        return 'unsettled'
    if snapshot.get('oom') is not False:
        return 'abnormal-terminal'
    if not isinstance(receipt, dict):
        return 'terminal-without-receipt'
    if (receipt.get('schema') != 'bounded-job/v1' or receipt.get('nonce') != expected_nonce
            or receipt.get('complete') is not True
            or type(receipt.get('driver_exit')) is not int
            or type(receipt.get('payload_exit')) is not int
            or not 0 <= receipt['driver_exit'] <= 255
            or not 0 <= receipt['payload_exit'] <= 255
            or type(snapshot.get('exit_code')) is not int
            or snapshot['exit_code'] != receipt['driver_exit']
            or type(receipt.get('cancelled')) is not bool):
        return 'invalid-receipt'
    if not receipt['cancelled'] and receipt['driver_exit'] != receipt['payload_exit']:
        return 'invalid-receipt'
    if receipt['cancelled']:
        return 'cancelled'
    if receipt['payload_exit'] == 124:
        return 'workload-timeout'
    if receipt['payload_exit'] >= 128:
        return 'payload-signalled'
    if receipt['payload_exit'] in (125, 126, 127):
        return 'driver-or-exec-error'
    return 'payload-finished'  # The Root-bound JUnit/results checks still decide QA.
