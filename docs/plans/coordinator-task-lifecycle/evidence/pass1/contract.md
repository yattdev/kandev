# Guarded Coordinator task lifecycle metadata

Support request `5e629b49-3b50-472f-85d6-54a8382fa515`.
Status: escalation pass 1 qualifies a restart-free per-request module route.
Installation and installed synthetic acceptance are recorded in
[the activation evidence](task-lifecycle-pass1/README.md). Root owns the one
separately bound live acceptance read; Support performs no target census.

## Contract

```sh
docker kandev task-lifecycle <task-UUID>
```

Uses the existing wrapper, task-scoped HMAC and broker Unix transport. Accepts
only a target UUID. The broker authenticates the kernel peer against its exact
active registered launch, task/session/incarnation/environment and Coordinator
repository/workspace, then rechecks those identities before releasing a result.
No session arguments, identity environment overrides, token changes, alternate
actor or unique-session fallback are accepted.

Success returns bounded JSON (`schema: task-lifecycle/v1`) with task ID,
workspace ID, current workflow/step IDs, state, update/archive timestamps,
`lifecycle: active|archived` and plan presence. `active` means not archived; it
does not mean a session is running. Missing plans are
`plan: {status: absent, code: PLAN_NOT_FOUND}`; retained plans are
`plan: {status: present, code: null}`. No document body, title, path, session
inventory, customer content, runtime configuration, queue or credential is read.

Errors exit 78 with typed JSON on stdout and a fixed diagnostic on stderr:

| Code | Meaning |
| --- | --- |
| `INVALID_ARGUMENT` | Requires one full target UUID. |
| `ACCESS_DENIED` | Exact active Coordinator caller proof failed or changed. |
| `TASK_NOT_FOUND_OR_INACCESSIBLE` | No visible row in the bound workspace; `deletion_evidence: unproven`. Foreign and missing responses are identical. |
| `METADATA_UNAVAILABLE` | Storage, reviewed dependency, response bound or audit failed. |

There is no deletion tombstone in this reader. Missing does not establish
deleted, archived or moved. Same-owner foreign workspaces remain inaccessible.
Same-workspace workflow movement is reflected only as current placement.

The read uses a read-only metadata connection and one target snapshot. It
performs no Docker/process launch, guard filesystem probe, provider call or
board/data mutation. A fixed audit event records caller task/session/execution,
workspace, target UUID and outcome. Output is at most 4 KiB.

## Reviewed dependency and tests

The new route reuses `process`, `ancestry` and `metadata` from the installed
platform-owned `kandev-test-worker.py`, with SHA256 pinned to
`6a09ebeb96429f955546cf8b30616cd287cf92fe36156ea54f7d7b843ad78a96`.
Its custody must be root-owned, non-writable by agents, single-link and stable
during loading. No worker service, policy, lease or registration is activated.
Dependency drift fails closed and requires source/test review.

`python3 tests/test-task-lifecycle.py` passes 22 synthetic cases, including
active/archived/planless/missing, both foreign-workspace cases, caller
revocation/mismatch/ambiguity, the real wrapper/client/HMAC/socket-peer dispatch,
exited-peer pidfd denial, PID/incarnation changes, bounded output and
storage/audit failures. Spoofed peer/session/workspace payload fields do not
supply identity. Before/after fixture dumps prove read preservation.

Tests run in private user and network namespaces with an empty filesystem root.
Live `/data`, `/run` and `/app` are hidden. Public platform scripts and a copied
public Python interpreter form a synthetic backend/agent ancestry boundary.
The fixture uses the actual launch validator, process ancestry, file custody,
registered Coordinator proof and read-only SQLite queries, with zero
substitutions for those authorization functions. The engine boundary is an
assertion that no command executes. Installed mode uses the installed files,
including their real ownership/modes; source mode stages reviewed source into
private root-owned files. This is synthetic qualification, not owner acceptance.

Existing image metadata, volume preservation, broker policy and task-access
regressions pass. See the recorded test outputs and preserving-superset check.

## Source and installed status

Both deployment and product source checkouts were inspected. Canonical source
was fetched and verified at `8c4fd38852700a68272502cef39f46d122e7397c` before
creating the isolated `support/task-lifecycle-5e629b49` worktree.
That branch contains the native plan-error repair design and synthetic
characterization; production product-source code remains unchanged.

The existing public `GET /api/v1/tasks/:id` can return archived metadata, but
is not an available task-bound Coordinator tool. `list_tasks` omits archived,
ephemeral and automation-origin records. The current Coordinator item tool
supports `proposal` and `stall`, not `task`. Native plan read error mapping
still turns missing/inaccessible-task and permission failures into
`INTERNAL_ERROR`; this MCP reader is unchanged. The new typed lifecycle reader
supplies the guarded metadata and `PLAN_NOT_FOUND` alternative without changing
document permissions or inferring any live-ID status. The product-native error
mapper follow-up remains separately designed, not required to use this route.

Deployment observed during this activation (identity/start time unchanged across
module replacement; restart count zero):

- Container: `8409b870b5ce55ce6e1a74203c0844d15085785a4a30fe944c5088c424983b57`.
- Image: `sha256:9a7b489e428439e090157cb96e482f8e21426ac6f75ae27402dee19fd3b3c8d4`.
- Start: `2026-11-25T20:31:11.237656793Z`; restart count zero.
- Version: `v0.101.0`; official source tag resolves to
  `f4484e17d99b805cd980ade543dbd4e2c5d6c4f8`. Version/tag association alone is
  not an independent build attestation for these binary hashes.
- Backend SHA256: `6d234280b9f0b757e6a2f12ab7d1708ba79cb3440faed9f0e28a1e6db855bf8b`.
- Agentctl SHA256: `933d38c1551698c7bd761710d91d00dd2c30f2adaf992858a771609435aa0917`.
- Linux helper SHA256: `98d7dba49bbbc0ef6571fa201915c052940ff79f4959903b508445a124d15675`.
- **Unchanged installed broker** SHA256:
  `9514b692d9833fcc64c1978b7553c181c37e84625e645a0ad0521d4b2f298e91`.

The earlier source-only prototype added `workspace task-lifecycle` to the
long-running broker, which would have required a prohibited restart. Escalation
identified that the unchanged broker already forwards unknown operations with
its original `FramedInput` Unix stream to a per-request module. The revised
`kandev task-lifecycle` route derives the actual peer using `SO_PEERCRED` and
`SO_PEERPIDFD` on that stream. No caller PID from payload, numeric pidfd fallback,
identity repinning, worker service or new credential is used.

The dormant broker prototype is removed; source broker SHA256 now equals the
unchanged installed broker. Only the root-owned operations module is replaced
atomically through the existing hash-gated installer. The candidate preserves
all preexisting handlers, including the installed immutable-image-ID fix,
volume preservation and worker routes. The source AST comparison and regression
results are retained with the review manifest. No backend/agentctl/helper binary
or image is rebuilt or replaced, and no process is restarted.

The request's Coordinator task/session/workspace IDs are supplied context only.
Support does not read its private worktree, enumerate the 27 targets, inspect
their records, repin an identity or clear G1. The finite next action is Root's
one separately bound read with the command above in its unchanged authenticated
Coordinator session. A missing/inaccessible result remains deletion-unproven.
