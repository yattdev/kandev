---
spec: docs/specs/tasks/requirements/coordinator-task-lifecycle.md
created: 2026-11-25
status: draft
---

# Coordinator task lifecycle read: implementation plan

Support request `5e629b49-3b50-472f-85d6-54a8382fa515`.
The requested guarded metadata alternative is implemented, installed and
synthetically qualified without restart. Product Go/native MCP error mapping
remain unchanged; task 01 is a separately designed optional follow-up. Exact
deployment patch and installed evidence are published with this package.
The existing isolated worktree retains verified canonical `upstream/main` base
`8c4fd38852700a68272502cef39f46d122e7397c`.

## Existing routes and diagnosis

`internal/task/handlers/task_http_handlers.go:httpGetTask` and
`internal/task/service/service_tasks.go:GetTask` can read archived tasks.
The public HTTP route is `GET /api/v1/tasks/:id`; its broad DTO and user/workspace
permission boundary do not constitute an available task-bound Coordinator tool.
Do not give Root a PAT, hidden API, operational SQL or alternate actor.

`internal/task/repository/sqlite/task.go:ListTasks` excludes archived, ephemeral
and automation-origin tasks. Omission is therefore not deletion evidence.
`GetTask` includes archived rows. `DeleteTask` removes the row; this reader has
no authoritative per-task deletion tombstone. Foreign and missing IDs remain
indistinguishable at the established authorization boundary.

`internal/task/planws/errors.go:GetError` currently falls through to
`INTERNAL_ERROR` for `repository.ErrTaskNotFound`, `service.ErrForbidden` and
`service.ErrTaskPlanNotFound`. This is verified on the canonical source and is
also present at deployed release tag `v0.101.0`, source
`f4484e17d99b805cd980ade543dbd4e2c5d6c4f8`. It explains a possible error path;
no conclusion is drawn about any of the request's 23 failing live IDs.

The latest `get_coordinator_item_kandev` accepts only `proposal` and `stall`;
it explicitly rejects `kind: task`. It is not a lifecycle route. The deployment
broker's `workspace probe` runs filesystem checks and is not a substitute.

## Smallest supported delivery route

The deployment-source implementation adds one command to the existing broker:

```text
docker kandev task-lifecycle <task-UUID>
```

This preserves the current legacy Coordinator, wrapper, task token and socket;
it does not migrate the caller to the newer native Coordinator principal.
The command is installed and synthetically qualified. Root's separately bound
live acceptance remains pending and is not performed by Support.

### Broker authorization and metadata

The unchanged deployed broker `handle_request` forwards unknown operations
with `FramedInput(self.rfile)` to `runpy.run_path` on every request. The
operations module adds `TaskLifecycle` and the exact `kandev task-lifecycle`
branch. It derives PID/UID using `SO_PEERCRED` and the original peer's pidfd
using `SO_PEERPIDFD` on that same socket. Payload fields cannot provide identity;
there is no numeric pidfd fallback. Liveness is checked before authorization,
around caller revalidation and immediately before output.

Reuse `coordinator_context(task_root)` for single registered Coordinator
repository and workspace eligibility. Reuse the read-only `process`, `ancestry`
and `metadata` functions in pinned `scripts/kandev-test-worker.py` with actual
kernel ancestry. No request identity hints, registration mode, leases, policy
changes or worker service are used. The root-owned single-link stable custody
loader validates that exact dependency before use. The existing functions
resolve active task/session/environment/execution using registered launch PIDs.

Require caller task/workspace to equal the Coordinator context. Record process
start identity and revalidate it and the selected launch immediately before
release of the result; fail closed on exit, PID reuse, ambiguous ancestry,
revocation, or changed incarnation. Add no fallback to a sole session when the
peer cannot be proven. Retain outer-guard Git backlink validation and existing
broker HMAC/root/cwd checks; do not move them into caller-controlled parameters.

Open Kandev metadata read-only via the broker's existing database boundary.
Within one read snapshot, select only the fixed task fields below with BOTH
task ID and authenticated workspace ID predicates, and plan existence with
`EXISTS` after eligibility. Do not load plan content, enumerate tasks, query
foreign placement to diagnose a missing ID, or inspect worktrees/runtimes.

Successful stdout JSON:

```json
{"schema":"task-lifecycle/v1","task_id":"<uuid>","workspace_id":"<bound uuid>",
 "lifecycle":"archived","state":"<stored state>","workflow_id":"<id or null>",
 "workflow_step_id":"<id or null>","updated_at":"<timestamp>",
 "archived_at":"<timestamp or null>","plan":{"status":"absent","code":"PLAN_NOT_FOUND"}}
```

Present plans use `{"status":"present","code":null}`. Success exits zero.
Failures exit 78 with a bounded stdout JSON error envelope preserving `schema`
and `error.code`, and fixed stderr text. Codes: `INVALID_ARGUMENT`,
`ACCESS_DENIED` (caller proof denied, before target access),
`TASK_NOT_FOUND_OR_INACCESSIBLE` (details `deletion_evidence: unproven`), and
`METADATA_UNAVAILABLE` (storage fault). Missing and foreign target envelopes
must be byte-identical apart from the caller-supplied target UUID.

### Native plan error repair

In canonical `internal/task/planws/errors.go:GetError`, preserve current safety
and input-validation mappings, then map wrapped missing-task and missing-plan
sentinels to `NOT_FOUND`, `service.IsForbidden` to `FORBIDDEN`, and all unknown
storage errors to the existing fixed `INTERNAL_ERROR`. Use stable details
reasons `TASK_NOT_FOUND_OR_INACCESSIBLE`, `PLAN_NOT_FOUND`, `ACCESS_DENIED`.
In `internal/mcp/handlers/handlers.go:handleGetTaskPlan`, return the missing-plan
mapping for an authorized nil result. Keep browser `wsGetTaskPlan` nil success
unchanged. Preserve partial-read/version behavior and existing document access;
do not authorize by plan presence or broaden ancestor/sibling policies.

## Tests

Task 01: synthetic Go handler/service/repository paths cover nil plans,
retained archived plans, wrapped sentinels, forbidden and real storage faults.
Task 02: synthetic broker Unix transport/peer tests cover valid exact launch,
ordinary caller, missing/ambiguous/stale/mismatched proofs, PID reuse, revoked
session, wrong root, both foreign-workspace cases, masked absence, malformed ID,
plan presence/absence and storage fault. Snapshot all synthetic tables and
archive/plan fields before/after lookup; intercept every Docker/provider/file
mutation boundary and require zero calls. Use real SQLite fixture metadata and
read-only query mode, not a mock boolean that substitutes for authorization.

No UI change or browser E2E is needed. The optional native reader follow-up has its own integration tests. The
installed metadata route and public client/Unix transport pass their synthetic
integration checks. No live task runtime or full deployment startup is part
of this delivery.

## Sequence and status

1. [Task 01: native typed plan errors](task-01-plan-errors.md) — pending optional
   native-reader follow-up; no product Go change is claimed.
2. [Task 02: guarded lifecycle metadata](task-02-guarded-lookup.md) — done;
   22 source and installed cases pass with no authorization mocks.
3. [Task 03: preserving exposure](task-03-exposure.md) — done via existing
   per-request module, without restart. Root's live acceptance stays separate.

## Verification results

[Current Support evidence](evidence/README.md) binds the exact source, test,
installed bytes, preserving-superset check and activation receipt. All existing
broker, image, volume and task-access regressions pass. Both source and installed
lifecycle fixtures use actual authorization functions and private synthetic
records; live data/runtime are hidden. Root's current session or target IDs
are not used as a Support operational proxy.

## Gates and limits

The earlier restart blocker overlooked the original socket stream passed into
the existing hot module loader. The final `kandev task-lifecycle` route derives
kernel peer PID/UID/pidfd and reuses exact active registered launch proof without
changing any caller role, credential or target workspace grant. Its module is
atomically installed through the reviewed existing installer. No server, broker,
container or binary lifecycle operation is needed or performed.

Task 01 remains subject to the product source design/release workflow and does
not gate this alternative metadata reader. Raw native plan errors are unchanged
and do not prove deletion. Finite next action: Root performs one separately
bound lookup using the installed route in its unchanged session/workspace.
No G1 clearing, board census, task mutation or polling is requested.
