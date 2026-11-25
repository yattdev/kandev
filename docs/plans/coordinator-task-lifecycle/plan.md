---
spec: docs/specs/tasks/requirements/coordinator-task-lifecycle.md
created: 2026-11-25
status: draft
---

# Coordinator task lifecycle read: implementation plan

Support request `5e629b49-3b50-472f-85d6-54a8382fa515`.
This is a product-source design handoff, not an installed capability. No
product Go or permanent Go test changes have been made. The independent
deployment-broker source repair is implemented and tested on its own `main`;
its exact patch is retained with this package. Source base is freshly fetched
canonical `upstream/main` at `8c4fd38852700a68272502cef39f46d122e7397c`.

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
docker kandev workspace task-lifecycle <task-UUID>
```

This preserves the current legacy Coordinator, wrapper, task token and socket;
it does not migrate the caller to the newer native Coordinator principal.
The command MUST NOT be offered as available until installed
and separately accepted. No flags or alternate wrapper can enable it today.

### Broker authorization and metadata

In deployment `scripts/kandev-agent-docker-broker:handle_request`, an exact
workspace operation branch before the existing workspace handler forwards the
kernel `SO_PEERCRED` PID already captured by `BrokerHandler`.
The old `handle_coordinator_workspace` does not receive this PID; its
task-root HMAC alone is insufficient proof of an exact caller session.

The same broker adds `handle_task_lifecycle`, `lifecycle_caller`,
`lifecycle_peer_identity`, `lifecycle_target` and `lifecycle_error`. Reuse
`coordinator_context(task_root)` for single registered Coordinator repository
and workspace eligibility. Reuse the read-only `ancestry`/`metadata` launch
validation in `scripts/kandev-test-worker.py`, with `ancestors` required and
without request-supplied identity hints, registration mode, lease creation,
worker policy changes or worker service activation. The functions already
resolve exact active task/session/environment/execution identities using
registered metadata and launch PIDs. Load only the installed platform-owned
module under the existing custody checks; do not execute task-owned modules.

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

No UI change or browser E2E is needed. The native plan reader and broker's public
client/Unix transport both require integration tests. Planned exact checks are
listed in each task. No full deployment suite or runtime startup is part of
this source-only delivery.

## Sequence and status

1. [Task 01: native typed plan errors](task-01-plan-errors.md) — pending.
2. [Task 02: guarded lifecycle metadata](task-02-guarded-lookup.md) — source done,
   20 synthetic cases plus existing broker policy regression passed.
3. [Task 03: preserving exposure and acceptance](task-03-exposure.md) — pending,
   requires separate activation authority. No delegation is authorized.

## Verification results

See [the Support evidence](evidence/README.md) for exact source/build identities,
temporary characterization source and results. Go characterization establishes
current-reader behavior; the deployment Python suite establishes source-only
lookup conformance. Installed/caller-bound acceptance remains pending. Temporary Go characterization files
are removed before commit; production Go files remain unchanged.

## Gates and risks

The source AGENTS.md and `.agents/skills/spec-driven-development/SKILL.md`
require a later explicit implementation request after this package, or an
explicit planning opt-out for product Go/permanent Go test changes. Neither is
present in the incoming request. This does not block the separately scoped
deployment implementation, which is complete in source.

The new broker dispatch branch changes long-running loaded code; hot-copying
a module alone cannot expose it. Restart/reload is forbidden by this request.
No new trust grant, broker restart or Kandev recreate is performed. A future
reviewed activation must preserve all installed script fixes and any required
binary overlays; never deploy just a newer backend. Any Kandev image deployment
must use `scripts/kandev-safe-deploy` and HTTP-200 rollback verification.

Next input for the completed broker source: reviewed activation authority for
the exact dispatch and pinned validator, followed by Root's one bound read.
The native error mapper requires a separate implementation continuation naming
task 01 and eventual normal release. No session repinning, census or task action
is part of either route.
