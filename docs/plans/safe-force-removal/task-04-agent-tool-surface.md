---
id: "04-agent-tool-surface"
title: "Agent tool surface"
status: pending
wave: 4
depends_on: ["03-shared-service-http-api"]
plan: "plan.md"
requirements:
  - REQ-TASKS-SAFE-FORCE-REMOVAL-002
system_design:
  - ../../specs/tasks/system-design/safe-force-removal.md
---

# W04: Agent tool surface

Register target-grant, preview, and execute tools that derive caller identity
from trusted transport context. Test forged bodies, foreign targets, expiry,
single use, and replay.

## Verification

```bash
(cd apps/backend && go test -count=1 ./internal/mcp/... ./internal/task/service)
```
