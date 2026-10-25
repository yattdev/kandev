---
status: draft
system: tasks
requirements:
  - REQ-TASKS-ACTIVE-QUEUE-CENSUS-001
---

# Active-owner queue census design

## Boundary and current evidence

This design is pinned to canonical main
`cc687bdce96bc22fb8a9ca87abe571b8309d3125`, resolved on 2026-10-25 by
`git ls-remote origin HEAD refs/heads/main`. It adds an observational projection
inside existing queue repositories and the task MCP path. It adds no authority
store, scheduler, queue engine, schema migration, grant or public targeting API.

Current source anchors, all at that commit:

- [Trusted stream execution](https://github.com/kdlbs/kandev/blob/cc687bdce96bc22fb8a9ca87abe571b8309d3125/apps/backend/internal/agent/runtime/lifecycle/mcp_identity.go#L87)
  attaches `streams.MCPExecutionContext`; its `ExecutionID`, task and session
  cannot originate in a tool payload.
- [Scope resolver](https://github.com/kdlbs/kandev/blob/cc687bdce96bc22fb8a9ca87abe571b8309d3125/apps/backend/internal/mcp/scope/scope.go#L112)
  and [principal](https://github.com/kdlbs/kandev/blob/cc687bdce96bc22fb8a9ca87abe571b8309d3125/apps/backend/internal/mcp/scope/principal.go#L48)
  supply owner and surface identity. Keep auth-disabled compatibility within
  that existing contract; trusted execution and exact pair remain mandatory.
- [Execution validation example](https://github.com/kdlbs/kandev/blob/cc687bdce96bc22fb8a9ca87abe571b8309d3125/apps/backend/internal/mcp/handlers/plugin_tools.go#L153)
  compares `executors_running.AgentExecutionID` and status with the stream,
  then checks session and task. Reuse the existing types, not plugin authority.
- [Pair guard](https://github.com/kdlbs/kandev/blob/cc687bdce96bc22fb8a9ca87abe571b8309d3125/apps/backend/internal/orchestrator/service.go#L2404)
  precedes orchestrator dependencies. Its legacy best-effort missing-row behavior
  is insufficient alone: the new entry point also requires positive row evidence.
- [Queue snapshot](https://github.com/kdlbs/kandev/blob/cc687bdce96bc22fb8a9ca87abe571b8309d3125/apps/backend/internal/orchestrator/messagequeue/repository_sqlite.go#L2386)
  uses the writer, touches the task row, locks the queue and advances
  `status_generation`. [Memory snapshot](https://github.com/kdlbs/kandev/blob/cc687bdce96bc22fb8a9ca87abe571b8309d3125/apps/backend/internal/orchestrator/messagequeue/repository_memory.go#L155)
  also advances a counter. Neither is a read-only census implementation.
- [Service visibility](https://github.com/kdlbs/kandev/blob/cc687bdce96bc22fb8a9ca87abe571b8309d3125/apps/backend/internal/orchestrator/messagequeue/service.go#L3405)
  defines pending eligibility. `GetStatus` can return empty after a read failure;
  it is not an acceptable fallback.

The current server/handler source has no registered active-owner census.
The preserved synthetic consumer report at old main `3eb6728` is historical
gap evidence only; no test or deployed-runtime verdict is inherited here.

## Contribution boundary

[Issue #4758](https://github.com/kdlbs/kandev/issues/4758) already owns the request;
as read on 2026-10-25 it is open with zero comments. No duplicate issue, question,
PR or reviewer ping is part of this package. The maintainer's
[#3359 scope decision](https://github.com/kdlbs/kandev/issues/3359#issuecomment-6155733917)
and [#3377 closure](https://github.com/kdlbs/kandev/pull/3377#issuecomment-6133099114)
require a focused missing primitive against current contracts. They neither
accept this contribution nor permit reviving the broad coordinator proposal.

Frozen source `fea9cd0554e230134c86865ad9309f0fe70fd576` is preserved in an own
recovery ref and complete bundle. Its `messagequeue/census.go` contains hashes,
claims, sender/time fields and mutation revision requirements; its server
`message_queue_census.go` registers both read and removal tools. Reuse only
reviewed hashing, strict empty-object validation and encoded-result bounding
ideas. Do not cherry-pick those files or their 66-file dependency stack.

## Requirement mapping

| Criteria in REQ-TASKS-ACTIVE-QUEUE-CENSUS-001 | Design sections |
| --- | --- |
| .1, .3 | MCP contract |
| .2, .4 | Authority and snapshot fence |
| .4, .5, .7 | Queue projection and revision |
| .6 | Read-only invariants |
| .8 | Race semantics and documentation |

## MCP contract

The public name is `get_message_queue_census_kandev`. Register a separate
profile group for `SurfaceKanbanTask` and `SurfaceOfficeTask`; do not put it in
`registerKanbanTools`, which is also composed into broader surfaces. Add a
read-only annotation. Require `{}`; reject unexpected/duplicate keys and scalar,
array or null payloads before backend dispatch. No task, session, execution,
incarnation, owner, claim, offset or limit is accepted from the caller.

The backend action is `mcp.message_queue_census`. It must repeat
surface and trusted-context validation for raw actions. Handler dispatch uses
the existing trusted execution/principal context, not new identity fields.
Reach the orchestrator through a narrow read capability on the existing
`sessionLauncher`; an unsupported dependency returns unavailable, never legacy
status. The orchestrator method is `GetActiveQueueCensus`.

Use a typed result with exactly these fields:

```json
{
  "version": 1,
  "task_id": "own-task",
  "session_id": "own-session",
  "session_incarnation_id": "current-incarnation",
  "snapshot_revision_sha256": "64 lowercase hex characters",
  "complete": true,
  "count": 0,
  "capacity": 0,
  "entries": []
}
```

Each entry has only `id`, `ordinal`, `content_sha256` and `content_bytes`.
Ordinals start at one and reflect existing `(position, id)` FIFO order. Hash
exact content bytes with SHA-256 without normalization. Capacity comes from
`Service.MaxPerSession`; it is not queue depth. Reject an unrepresentable result
at both the typed boundary and final encoded MCP result boundary, including
escaping overhead. Never serialize a `QueuedMessage` as this result.

## Authority and snapshot fence

1. Before queue lookup, require nonempty trusted execution ID/task/session and
   matching principal task/session, an allowed surface and current owner scope.
   Invoke the existing task/session pair access check first in the orchestrator.
2. Positively load the task, session and `ExecutorRunning` through existing
   authorized readers. Require unarchived task, exact task/session/workspace,
   session RUNNING, nonempty `QueueIncarnationID`, and RUNNING executor whose
   `AgentExecutionID` equals the trusted stream execution ID. Missing rows,
   unavailable checkers or legacy empty identities deny. Never ensure, resume
   or create an execution for this read.
3. Capture an internal proof of those observed values and current owner scope.
   This is process-local request data, not a new trust source or exported token.
   Call the existing queue service with this exact identity.
4. In SQL, validate the task/session/incarnation and execution/owner binding
   using SELECT predicates in the same read snapshot as the queue projection.
   Do not use `guardActiveTaskTx`, `lockSessionTx`, `validateSessionIdentityTx`
   (PostgreSQL adds FOR UPDATE), or `ResolveSessionIdentity` as a read repair.
   An active transfer fence or inconsistent schema/evidence is unavailable.
5. Revalidate owner/account scope and the exact live execution/session binding
   before publishing the encoded result. If the proof changed or cannot be
   reread, discard the whole result. No automatic retry or alternate target.

SQL snapshot predicates close the task/session/execution mismatch within the
read. The final recheck prevents knowingly releasing results from an observed
revocation. It is not an atomic cross-store revocation transaction; a change
after the last successful check can race response delivery, as for other reads.
No stronger revocation or all-writer fencing claim is made.

## Queue projection and revision

Add an internal bounded census method to the existing `Repository` and
`Service`; implement it in focused new files for the existing memory and SQL
providers. Return body-free descriptors, not full rows. Do not change writers.

For SQL, use the supplied `r.ro` reader and a genuine read-only transaction.
SQLite must use the existing deferred reader, not a writer with ReadOnly
options. PostgreSQL uses READ ONLY REPEATABLE READ for a multi-statement
consistent observation. Use native transaction ownership and cancellation,
without a pool, retry facade, writer row touch or new lock framework. The
[SQLite transaction ADR](../../../decisions/2026-10-05-sqlite-writer-transaction-admission.md)
explains why writer transaction options are insufficient.

Fetch no more than 1025 narrow row headers in FIFO order. Reject overflow;
compute visibility using the existing reserved/comment/attempted semantics.
Bound raw visibility metadata at 32 KiB per row and total examined content plus
metadata at 8 MiB before materializing bodies for hashing. Use correct UTF-8
byte lengths (`length(CAST(content AS BLOB))` for SQLite and `octet_length`
for PostgreSQL). Validate all selected-row task ownership and incarnation.
Hash only admitted content from the same snapshot; do not load attachments.
Reject invalid UTF-8, duplicate IDs, ambiguous order and malformed visibility
metadata. All budget errors return no entry prefix or count.

Memory uses the existing repository mutex to inspect bounded entries and build
descriptors. It must not call `bindIdentityLocked`, identity resolution or state
clearing. Compare existing identity plus the supplied canonical authority;
missing binding denies. Use the existing authority callback as a read only,
without caching it into repository maps. Do not retain mutable references after
unlock or invoke external authority callbacks while holding the mutex. Final
authority revalidation remains required.

Compute `snapshot_revision_sha256` over a fixed typed canonical serialization of
version, exact task/session/incarnation, capacity and complete ordered descriptor
array. Domain-separate it as `active-queue-census/v1`. Count and ordinals must
agree with that array. This revision fences the returned observation against
mixed contents; it is not a monotonic counter or precondition for a write.
An A-to-B-to-A observation can reproduce the digest. No caller can redeem it.
Do not reuse `status_generation`, which advances on reads, or install the old
all-writer revision machinery. A future mutation feature needs a separate design.

## Read-only invariants

SELECT-only SQL and an unchanged memory state are acceptance gates, including
no status-counter advancement, identity binding, cache repair, task-row touch,
attachment cleanup, reservation, receipt, readiness check, event or runtime
action. Errors are bounded generic codes: denied/not found, unavailable,
resource exhausted, invalid request or cancelled. Storage details and bodies
never enter public diagnostics. Use sensitive-handler logging and check both
success and error logging paths; no new metrics or audit persistence are needed.

## Race semantics and documentation

The FIFO may change immediately after the snapshot. The tool neither
acknowledges delivery nor proves an absent entry was delivered. Repeated calls
do not block dispatch or acquire a claim. The same semantics apply to desktop,
phone and agent consumers because this package adds no rendered UI.

Update `docs/public/automation-and-mcp.md` only with implementation, as a small
reference section documenting surfaces, empty arguments, exact descriptors,
limits, errors and staleness. Do not advertise a draft as available now.

## Delivery gate

The [work order](../../../plans/active-owner-queue-census/task-01-read-only-census.md)
is pending. Root must approve the concrete current-main source slice before
production or permanent test edits. Maintainer contribution fit remains open.
No public write, deployment, live queue experiment or cleanup follows from this
design. No independent review, QA or runtime pass is asserted.
