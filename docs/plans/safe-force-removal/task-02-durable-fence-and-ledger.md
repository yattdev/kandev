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

## Verification

```bash
(cd apps/backend && go test -count=1 ./internal/task/repository/sqlite ./internal/task/service)
```
