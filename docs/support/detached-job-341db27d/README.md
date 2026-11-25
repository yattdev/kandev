# Bounded detached QA execution on the existing restricted route

Request `341db27d-80a1-482d-bbdb-2508e65ba996`.

**The existing restricted Compose route supports detached launch, retained
container exit state, own log/receipt files and exact-owned metadata/stop.**
No production patch, installation, restart, policy increase or new caller/profile
is required for that execution pattern. Qualification is private and synthetic;
Support has not inspected or run the affected workload or any live QA resource.

## Exact timeout diagnosis

The installed broker's `ONEOFF_RUN_TIMEOUT` is 600 seconds and applies to the
engine-facing `compose run` command. `run_command` waits for its CLI subprocess,
kills that subprocess on expiry, and returns 124 with the observed diagnostic.
It does not stop or remove the daemon's container. The ordinary client has no
separate 600-second response timer; its three-second transport-probe timer is
unrelated. Neither a 1200-second command inside the container nor a 1500-second
caller budget changes the broker constant.

The 600 seconds cover the attached run phase, including worker startup/setup,
not just PHPunit's own clock. The supplied 22:31:04 worker start, 22:32:37 test
start and 22:41:04 cutoff are consistent with that distinction. No live record
was read to infer a further result. Original r6 remains incomplete; a partial
progress count and zero-byte JUnit do not establish RED, PASS or failed IDs.

Other commands and guarded model rendering retain their existing 1800-second
`COMMAND_TIMEOUT` default. This is **not a universal 600-second server deadline**,
and the execution timer is not an absolute RPC deadline covering every phase.
No limit is changed here. This delivery uses short detached/metadata operations
with caller waits separately bound to at most 600 seconds. A hard server-wide
600-second deadline, including hung parsing/metadata, is not an already-exposed
contract and is not claimed by these tests. The optional clarification asked
about that distinction; this recipe proceeds with preserving installed policy.

## Root-owned preparation before any acceptance

Root binds a new scenario for task `d21b9ef3-e215-4853-b314-9a5276fcd432`, QA
session `ac5636a2-2352-44fe-b66f-e3ddbb867740`, exact root
`/data/tasks/8354-adapt-co-up-dat_driff94t` and alias `kd_t2qa_full366`.
Support did not inspect their private files or state.

Keep the exact previously admitted file list/project directory and model
binding `6a48c16688eb730a3cef14e8573da274e81bc62fe4a376367fd7ac2d7a6fb561`,
local PHP image `e17d69e181bab8e27e933ea0c736707074b327400f9e64f6cd1aaf8f28f179fc`,
source/lock/vendor bindings and existing mount/identity/network policy. Root
selects the actual service; `qa` in the synthetic fixture is not live evidence.
The selected service must be image-only with the already-local approved image,
no build or long lifecycle hooks. `--pull never` is retained; `--no-build` is
not a valid Compose run flag and is not part of this recipe.

The existing no-egress DB namespace must remain the one already bound to DB
`872839ed40529ee2dd6ad428d492d64836d1bf09b167cc22831f3d205b1de5c1`, with only
loopback. The fixture declares DB `network_mode: none` and worker `service:db`;
it does not prove the live namespace. Root binds UID `1000:999`, source/vendor
RO, root filesystem RO and only the existing own log mount RW. No arbitrary
container namespace, new network, ports, device, privilege, host path or secret
is added. Model policy and the immutable caller/project claim remain authoritative.

The DB is already populated and partially exercised. The old worker was stopped
and removed. This is not permission to replay schema import, migrations, RBAC,
fixtures, reseeding or the old full suite. Root must decide the valid next QA
scenario before providing a new opaque payload. All existing data/logs and old
QA resources remain untouched.

## Launch recipe

Root freezes [bounded-job.sh](bounded-job.sh), its hash, a fresh one-use nonce,
container log directory, the exact new payload argv, duration 1200 and kill grace
15. The task image must contain `/bin/sh`, GNU `/usr/bin/timeout`, `grep`, `date`,
`mkdir`, `mv` and `printf`, and the caller must be able to read its own new
receipts. The driver makes an inert GNU-timeout version check before the payload;
missing/incompatible tooling is an execution error, not permission to install
packages, fetch an image, switch credentials or retry. Support tested the public
fixture's GNU timeout, not the affected image's tool installation.

After Root authorizes the new scenario, use the same actor/environment and exact
existing Compose files. The following placeholders are bound by Root, not guessed:

```sh
test "$KANDEV_AGENT_DOCKER_ROOT" = /data/tasks/8354-adapt-co-up-dat_driff94t
cd "$KANDEV_AGENT_DOCKER_ROOT"
export COMPOSE_PROJECT_NAME=kd_t2qa_full366
# ROOT_DRIVER_TEXT is the exact frozen bounded-job.sh content, passed as argv.
# ROOT_EXISTING_MODEL represents the exact original approved file list.
KANDEV_TASK_DOCKER_ACCESS=restricted docker compose \
  --project-directory "$KANDEV_AGENT_DOCKER_ROOT" \
  -f "$ROOT_EXISTING_MODEL" \
  run --detach --no-deps --pull never --interactive=false -T --entrypoint sh \
  "$ROOT_SERVICE" -c "$ROOT_DRIVER_TEXT" bounded-job \
  "$ROOT_NONCE" "$ROOT_CONTAINER_LOG_DIR" 1200 15 \
  ROOT_APPROVED_PAYLOAD_AND_ARGUMENTS
```

If the original invocation used multiple files, preserve that exact list instead
of the single placeholder above. Do not alter its model as a registration trick.
Bind a short caller budget <=600 for launch; the successful detached response is
only a full 64-hex container ID, not the test exit code. Reject malformed/mixed
stdout and save the exact ID with the fresh nonce and frozen command/model
hashes in the caller's control record. The driver is passed through `sh -c`
argv: Compose run does not stream a piped shell script through this client.

Omit `--rm` so terminal daemon state remains readable. Do not use `--name`
(the broker forbids it), `--build`, new volume/network options, service ports,
`up`, a special profile, raw Docker/context/socket or alternate credentials.
Compose one-off runs disable service restart policies; Root also verifies that
no automatic restart is configured in the resulting metadata.

## Receipts and finite observation

The driver atomically claims `<logs>/<nonce>.job`; a duplicate exits before the
payload. It writes `start.json`, separate stdout/stderr logs and, after reaping
the timeout process, an atomic `exit.json`. It returns the recorded driver code.
`timeout` bounds the payload to 1200 seconds plus at most 15 seconds kill grace
for a TERM-ignoring child. Its parent remains available to record that hard kill.
A trapped stop is relayed to the child and recorded as cancellation. SIGKILL/OOM
of the parent can prevent receipt writing; a missing receipt is never success.
The worker exits independently of the launch client's lifetime.

Observe the own control receipt with a finite count and absolute deadline bound
by Root. Those are active caller observations, not a new timer, routine, scheduled
watcher, launch retry or provider/fixture-data poll. No Docker call should wait
for the 1200-second payload; do not use long `wait`, attached run or log-follow.
The job's lifetime is not bounded by holding a broker connection open.

For each short identity/state observation, use the returned exact ID and the
fixed [status-format.txt](status-format.txt), with a short caller budget <=600
(for example 30 seconds), in the unchanged restricted environment:

```sh
KANDEV_TASK_DOCKER_ACCESS=restricted docker inspect --type container \
  --format "$ROOT_FROZEN_STATUS_FORMAT" "$ROOT_RETURNED_CONTAINER_ID"
```

The existing guard validates its task/project/service ownership before forwarding
that projection. The template returns only ID, state/exit, OOM, timestamps,
auto-remove/restart and the bound image/user/RO/network values. It returns no
environment, source contents, credentials, host PID or unrelated resources.
Root compares image config ID, UID, RO rootfs and `container:<bound DB ID>` to its
already approved policy. The image config ID is not a registry digest.

[receipt_state.py](receipt_state.py) qualifies conservative reconciliation:
while running, an exit-code field of zero is not completion. Require terminal
`exited`, matching exact ID/nonce, no OOM, matching driver/daemon exit and a valid
complete receipt. Stale, absent, malformed, policy-mismatched or contradictory
records are incomplete/uncertain. A normal payload exit is still not a QA verdict;
Root must validate the full JUnit/results from this new nonce before declaring
RED/PASS. Exit 124 in a payload receipt means its own timeout; caller/RPC 124
means an uncertain call and says nothing about worker settlement.

Caller deadlines bound the wait, not a new server-wide cancellation guarantee.
If launch or an observation expires, do not automatically launch again or infer
that the worker stopped. Root reconciles the exact admitted job under a separate
finite settlement allowance. If the new job must be cancelled, the already
supported exact-owned command is:

```sh
KANDEV_TASK_DOCKER_ACCESS=restricted docker stop --time 30 \
  "$ROOT_RETURNED_CONTAINER_ID"
```

Then verify terminal state/receipt for that same ID. A missing worker does not
prove a successful test. Do not stop the DB or remove volumes. Retention is
intentional; no cleanup is implicit. Any later removal needs its own exact-worker
binding after results/exit evidence are preserved. No global cleanup is proposed.

## Independent qualification and installed status

15 cases pass against installed public broker/module/wrapper/client and actual
Compose parsing/help, using private models/claims and a fake engine. Coverage
includes detach/no-deps/pull-never/TTY/stdin flags, unchanged 600 run budget,
wrong caller/foreign container rejection, own exact stop without DB/removal,
running-vs-terminal reconciliation, nonce collision, receipt mismatch/OOM,
normal exit, soft timeout 124, finite hard kill 137 and signal cancellation.
Short OS tests use 1-second deadlines, not the owner workload. Source mode passes
13 and skips the two installed-parser/transport cases. No source result is
represented as affected-owner acceptance.

The installed fixture uses an empty filesystem root and private network/user
namespaces; live `/data`, `/run`, `/app`, engine sockets and credentials are
absent. No owner image/model/resource inspection or test replay occurs.
The initial fixture asserted an explicit false mount field that real Compose
omits; it was corrected to check the exact target/mode mapping. Its failed output
is retained separately and is not called acceptance. Production code is unchanged.

Both checkouts were inspected. Product primary HEAD remains
`cfd6bdf65ae6f631d7701c105791a099c0325cc3`; unrelated source and deployment changes
are preserved. Installed broker is SHA256
`9514b692d9833fcc64c1978b7553c181c37e84625e645a0ad0521d4b2f298e91`, operations
`d60ccdfeb55ea4cca64e10d1e68639f7f1cafa0d6705b8cb78be6a20bc6d59ee`.
Full public-file readback is retained. No candidate build, installation, restart,
new profile, lifecycle candidate activation or policy increase is needed/performed.
Existing image/build/lifecycle incidents are not reopened by this delivery.

Version-bound primary Compose sources support the one-off details:
[CLI options/selection](https://github.com/docker/compose/blob/v5.7.1/cmd/compose/run.go)
for detach output, no-deps and stdin/TTY; and
[runtime implementation](https://github.com/docker/compose/blob/v5.7.1/pkg/compose/run.go)
for detached attachment, one-off restart suppression and lifecycle-hook behavior.
Actual flags/config are independently qualified by the installed parser; actual
engine/container behavior remains Root-owned acceptance, not a mock-engine claim.

**Finite next action:** Root reviews this qualified recipe and binds one new
scenario with the current populated DB state, fresh nonce, finite observation/
settlement budget and exact payload. The owner has no launch, retry, reset,
reseed, source edit or runtime authorization merely from this Support response.
