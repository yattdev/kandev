---
id: "02-durable-fence-and-ledger"
title: "Durable fence and ledger"
status: blocked
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

## Verification

```bash
(cd apps/backend && go test -count=1 ./internal/task/repository/sqlite ./internal/task/service)
```
