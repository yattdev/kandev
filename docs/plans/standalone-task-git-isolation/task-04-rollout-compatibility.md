---
id: "04-rollout-compatibility"
title: "Validate rollout compatibility"
status: pending
wave: 4
depends_on:
  - task-02-standalone-process-isolation.md
  - task-03-docker-mount-isolation.md
plan: "plan.md"
requirements:
  - REQ-EXECUTORS-TASK-GIT-ISOLATION-002
acceptance_criteria:
  - AC-EXECUTORS-TASK-GIT-ISOLATION-002.1
system_design:
  - ../../specs/executors/system-design/task-git-execution-isolation.md
---

# Task 04: Validate rollout compatibility

## Summary

Package the helper contract, document Linux compatibility, and verify fail-closed
diagnostics without recreating live sessions.

## In scope

- Package and runtime capability checks.
- Operator documentation and migration wording.

## Out of scope

- Deployment and automatic task-session recreation.

## Acceptance

- Unsupported hosts report an actionable failure before launch.
- Existing sessions are not mutated by rollout logic.

## Verification

```bash
(cd apps/backend && go test ./internal/agent/runtime/lifecycle ./internal/agentctl/server/process/... -count=1)
node scripts/validate-public-docs.mjs
```

## Files likely touched

- package and launcher manifests
- `docs/public/`
- executor compatibility tests

## Dependencies

Tasks 02 and 03.

## Risks

Distribution support differs by Linux package channel.

## Parallelism

`sequential`

## Inputs

- `task-git-execution-isolation.md`

## Results

Pending.
