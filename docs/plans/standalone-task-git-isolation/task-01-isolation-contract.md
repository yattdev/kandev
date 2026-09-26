---
id: "01-isolation-contract"
title: "Define the isolation contract"
status: pending
wave: 1
depends_on: []
plan: "plan.md"
requirements:
  - REQ-EXECUTORS-TASK-GIT-ISOLATION-001
  - REQ-EXECUTORS-TASK-GIT-ISOLATION-002
acceptance_criteria:
  - AC-EXECUTORS-TASK-GIT-ISOLATION-001.1
  - AC-EXECUTORS-TASK-GIT-ISOLATION-002.4
system_design:
  - ../../specs/executors/system-design/task-git-execution-isolation.md
---

# Task 01: Define the isolation contract

## Summary

Carry validated Git projections as a server-authored agentctl isolation manifest.

## In scope

- Typed lifecycle, agentctl request, and instance configuration contract.
- Canonical containment and malformed-manifest rejection.

## Out of scope

- Process wrapper execution and Docker mount conflict checks.

## Acceptance

- Each projection yields only task-private writable paths and shared read-only paths.
- Invalid, symlinked, swapped, or foreign metadata produces no manifest.

## Verification

```bash
(cd apps/backend && go test ./internal/worktree ./internal/agent/runtime/lifecycle ./internal/agentctl/server/config -count=1)
```

## Files likely touched

- `apps/backend/internal/agent/runtime/lifecycle/executor_standalone.go`
- `apps/backend/internal/agentctl/server/config/config.go`
- agentctl create-instance DTO and tests

## Dependencies

None.

## Risks

Internal request contracts must not permit agent-supplied path expansion.

## Parallelism

`sequential`

## Inputs

- `task-git-execution-isolation.md`

## Results

Pending.
