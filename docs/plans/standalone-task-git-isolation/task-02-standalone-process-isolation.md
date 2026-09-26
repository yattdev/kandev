---
id: "02-standalone-process-isolation"
title: "Enforce standalone process isolation"
status: pending
wave: 2
depends_on:
  - task-01-isolation-contract.md
plan: "plan.md"
requirements:
  - REQ-EXECUTORS-TASK-GIT-ISOLATION-001
  - REQ-EXECUTORS-TASK-GIT-ISOLATION-002
acceptance_criteria:
  - AC-EXECUTORS-TASK-GIT-ISOLATION-001.1
  - AC-EXECUTORS-TASK-GIT-ISOLATION-001.2
  - AC-EXECUTORS-TASK-GIT-ISOLATION-001.3
  - AC-EXECUTORS-TASK-GIT-ISOLATION-001.4
  - AC-EXECUTORS-TASK-GIT-ISOLATION-002.1
system_design:
  - ../../specs/executors/system-design/task-git-execution-isolation.md
---

# Task 02: Enforce standalone process isolation

## Summary

Wrap all agent-controlled standalone processes in the Linux task isolation
environment and prove direct sibling writes fail.

## In scope

- Packaged-helper invocation and fail-closed diagnostics.
- ACP, process-runner, and interactive-shell launch paths.
- Real Git and hostile-write integration coverage, including attached repos.

## Out of scope

- Docker final mount validation and deployment.

## Acceptance

- All agent-controlled launch paths use the same manifest.
- Direct shared-ref and reflog writes fail while native task Git succeeds.
- Missing helper or unsupported host capability prevents launch.

## Verification

```bash
(cd apps/backend && go test ./internal/agentctl/server/process/... ./internal/agent/runtime/lifecycle -count=1)
```

## Files likely touched

- `apps/backend/internal/agentctl/server/process/isolation/`
- `apps/backend/internal/agentctl/server/process/manager.go`
- `apps/backend/internal/agentctl/server/process/runner.go`
- `apps/backend/internal/agentctl/server/process/interactive_*.go`

## Dependencies

Task 01.

## Risks

The helper must be available and compatible on every supported Linux package.

## Parallelism

`sequential`

## Inputs

- `task-git-execution-isolation.md`

## Results

Pending.
