---
id: "01-plan-errors"
title: "Preserve typed MCP plan read failures"
status: pending
wave: 1
depends_on: []
plan: "plan.md"
spec: "../../specs/tasks/requirements/coordinator-task-lifecycle.md"
---

# Task 01: Typed plan read errors

Acceptance:

1. Missing/inaccessible target, authorized missing plan, known denial and storage
   failure return the exact codes/reasons in requirement 003 without leaking
   foreign-task existence or altering browser nil-plan success.
2. Source integration tests exercise authenticated handler/service/repository
   reads of synthetic active, archived, planless and denied targets; retained
   plan bytes, versions, revisions and archive timestamps do not change.

Likely files: `apps/backend/internal/task/planws/errors.go`, new focused
`plan_read_errors_test.go` beside it, `internal/mcp/handlers/handlers.go`, new
`plan_read_errors_test.go` beside that, and MCP server projection tests.
Dependencies: none. Execution: sequential. Inputs: spec requirement 003 and
plan's native repair section. Do not change caller authorization policy.

Verification from repository root:

```sh
cd apps/backend
go test ./internal/task/planws ./internal/mcp/handlers ./internal/mcp/server -run 'Plan(Read|Error|Partial|Task)|GetTaskPlan' -count=1
```

Update this task and plan with the exact tests, counts, changed files and trust
boundary evidence. No build/install/restart or live task reads are authorized.

## Results

Pending explicit implementation continuation. No production/permanent tests
changed during the design turn.
