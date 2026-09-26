---
id: "03-shared-service-http-api"
title: "Shared service and HTTP API"
status: pending
wave: 3
depends_on: ["02-durable-fence-and-ledger"]
plan: "plan.md"
requirements:
  - REQ-TASKS-SAFE-FORCE-REMOVAL-001
  - REQ-TASKS-SAFE-FORCE-REMOVAL-002
  - REQ-TASKS-SAFE-FORCE-REMOVAL-004
system_design:
  - ../../specs/tasks/system-design/safe-force-removal.md
---

# W03: Shared service and HTTP API

Implement no-store preview and exact commit through one authorized service,
reusing shared contracts only after dependency sync. Test denial ordering,
unknown workspace evidence, quiescence, and no physical cleanup invocation.

## Verification

```bash
(cd apps/backend && go test -count=1 ./internal/task/service ./internal/task/handlers)
```
