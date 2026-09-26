---
id: "03-docker-mount-isolation"
title: "Enforce Docker mount isolation"
status: pending
wave: 3
depends_on:
  - task-01-isolation-contract.md
plan: "plan.md"
requirements:
  - REQ-EXECUTORS-TASK-GIT-ISOLATION-002
acceptance_criteria:
  - AC-EXECUTORS-TASK-GIT-ISOLATION-002.2
  - AC-EXECUTORS-TASK-GIT-ISOLATION-002.3
  - AC-EXECUTORS-TASK-GIT-ISOLATION-002.4
system_design:
  - ../../specs/executors/system-design/task-git-execution-isolation.md
---

# Task 03: Enforce Docker mount isolation

## Summary

Reject final Docker mount sets that reopen a shared Git root and retain the
clone-inside no-host-projection boundary.

## In scope

- Mount overlap validation after all mount sources are combined.
- Unit and daemon-backed Docker coverage.

## Out of scope

- Standalone process wrapper behavior.

## Acceptance

- Runtime or plugin read-write shared-root mounts fail configuration.
- Exact task-admin child rebinds remain permitted.

## Verification

```bash
(cd apps/backend && go test ./internal/agent/runtime/lifecycle -count=1)
```

## Files likely touched

- `apps/backend/internal/agent/runtime/lifecycle/container.go`
- `apps/backend/internal/agent/runtime/lifecycle/container_config_test.go`
- Docker E2E fixtures

## Dependencies

Task 01.

## Risks

Daemon-backed coverage needs an approved isolated Docker environment.

## Parallelism

`sequential`

## Inputs

- `task-git-execution-isolation.md`

## Results

Pending.
