---
id: "02-durable-fence-and-ledger"
title: "Durable fence and ledger"
status: in_progress
wave: 2
depends_on: ["01-contract-and-dependency-sync"]
plan: "plan.md"
requirements:
  - REQ-TASKS-SAFE-FORCE-REMOVAL-003
  - REQ-TASKS-SAFE-FORCE-REMOVAL-004
system_design:
  - ../../specs/tasks/system-design/safe-force-removal.md
---

# W02: Durable fence and ledger

Implement the retained task state, immutable receipt ledger, cleanup hold, and
writer gates with SQLite/PostgreSQL CAS and idempotency tests. This begins only
after #3937 is merged and its shared contract is synchronized.

## Initial integration result

The merged guarded-retirement preview's closed predicate, receipt-status, and
redacted receipt shapes now live in `task/models`. The paired-preview service
retains source-compatible aliases. This creates one receipt vocabulary for the
future durable ledger without adding a force-removal route, claim, cleanup
call, or physical mutation.

The next bounded slice adds a private SQLite/PostgreSQL claim row keyed by the
exact task, workspace, task generation, admission generation, operation, and
request/preview digests. A matching replay returns the same claim; a foreign,
stale, or changed request is rejected. New cleanup jobs are held while the
claim exists. No route consumes this claim yet, and it does not hide a card or
invoke cleanup.

## Evidence and remaining work

Merged-preview receipt integration: `2c2fc143db819c64deea39fa04f1a5629bcab3fc`.
Private claim and cleanup hold: `32b7315a6ed9615143947908eed8072ea20f536c`.
The focused receipt and SQLite claim tests passed, and changed-package lint
passed in both commit hooks. A full `internal/task/service` and
`internal/task/handlers` run on the merged main baseline failed in unrelated
local-directory initialization tests with `parent directory cannot be accessed`;
it is not acceptance evidence.

Remaining W02 work: PostgreSQL claim parity; durable receipt rows; atomic
claim/replay/CAS behavior; and exact-task gates for launch, resume, message,
move, environment, worktree, queue, dispatch, cleanup, and PR-watch writers.
No route, card hide, runtime stop, or physical mutation is permitted before
those guards and their negative tests exist.

The PostgreSQL parity test covers exact replay and changed-request conflict
through the isolated-schema harness. It is skipped locally until
`KANDEV_TEST_POSTGRES_DSN` is supplied, so executed PostgreSQL evidence remains
an outstanding delivery gate.

The receipt ledger now appends ordered, redacted predicate evidence under the
private claim. The cleanup hold test also proves rejected cleanup admission
rolls back without leaving a job row.

The retained-state seam now uses the existing task creation barrier, so all
session and workspace-binding variants observe a private claim before creating
session or environment rows. The exact-claim insert is conflict-safe: one
concurrent identical request creates the claim and the other re-reads it as a
replay; a reused operation ID for another task is a conflict. The focused
SQLite package test suite passed after this change. `KANDEV_TEST_POSTGRES_DSN`
is absent in this task-owned checkout, so the isolated PostgreSQL parity test
is discovered and skipped locally rather than being run against CI or shared
state.

Remaining W02 work: extend the claim fence through message, move, environment
mutation, worktree, queue, dispatch, cleanup-worker, and PR-watch writers;
make receipt append ordering safe under concurrent appenders; and execute the
existing PostgreSQL-gated parity test with a task-owned DSN. There is still no
force-removal route, card hide, runtime stop, cleanup invocation, or physical
mutation.

The receipt ledger now locks its private claim before allocating an ordinal,
and one receipt per closed predicate is replay-safe: an exact retry preserves
the existing evidence while a changed retry conflicts. The append test starts
two distinct predicate writers concurrently and verifies all evidence remains.
The common message insert resolves task ownership from its session and rejects
a claimed task, while the full-row task writer used by workflow moves rejects
the claim after taking its existing source lock. Tests verify neither writer
persists its attempted mutation.

Writer inventory still open: task state/priority and other direct task
mutators; task-environment create/update; worktree materialization; queue
enqueue/dequeue and pending-move writers; dispatch and deferred-launch writers;
cleanup workers and retries; and PR-watch writers. Existing session creation
covers launch/resume and workspace-binding creation. PostgreSQL has an
isolated-schema harness in `internal/testutil.OpenIsolatedPostgres`, supplied
only through `KANDEV_TEST_POSTGRES_DSN`; this task-owned environment has no
such DSN or task-owned PostgreSQL service, so local PostgreSQL execution
remains unavailable and CI is the currently known isolated execution path.

Environment creation and workspace binding already use the task cleanup barrier
and therefore the claim fence. Environment update/delete paths now resolve and
lock the owning task before mutation. Cleanup worker activation for pending or
prepared jobs locks the owning task and refuses claimed tasks while preserving
the job state. The claim itself locks the task row on PostgreSQL, sharing that
serialization point with these writer gates. Focused tests prove that claimed
tasks keep their original environment and pending/prepared cleanup jobs with
no new environment or worker activation.

The remaining W02 writer inventory is queue enqueue/dequeue and pending-move
writers; dispatch and deferred-launch writers; PR-watch writers; direct task
state/priority and other narrow task mutators; environment-repository row
writers and materialization-finalization paths; plus cleanup retry/reset paths.
PostgreSQL execution remains blocked by the absent task-owned
`KANDEV_TEST_POSTGRES_DSN`; no supported local provisioning recipe or isolated
service is present in this checkout, and the CI postgres-boot service remains
the verified harness path.

## Verification

```bash
(cd apps/backend && go test -count=1 ./internal/task/repository/sqlite ./internal/task/service)
```
