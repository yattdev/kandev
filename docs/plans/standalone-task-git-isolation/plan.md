---
created: 2026-09-26
status: draft
requirements:
  - REQ-EXECUTORS-TASK-GIT-ISOLATION-001
  - REQ-EXECUTORS-TASK-GIT-ISOLATION-002
system_design:
  - ../../specs/executors/system-design/task-git-execution-isolation.md
legacy_specs: []
---

# Implementation Plan: Standalone Task Git Isolation

## Overview

Deliver strict Linux OS-enforced task Git isolation before validating Docker
mount conflicts. The sandbox manifest and all agent-controlled process entry
points come first because a metadata projection without a process boundary does
not meet the security requirement.

## Scope

### In scope

- Server-authored Linux isolation manifests for standalone worktree instances.
- Sandboxed ACP commands, user processes, and interactive shells.
- Host-visible normal Git operations and hostile direct-write denial tests.
- Multi-repository projection support and Docker final-mount conflict checks.

### Out of scope

- Non-Linux standalone support, deployment, and automatic live-session recreation.

## Technical approach

Add the typed manifest at the lifecycle-to-agentctl boundary, construct the
Linux helper command in `process/isolation`, then route each process launch
through it. Preserve the existing private-common Git arrangement. Validate final
Docker mounts after plugin/runtime expansion, never in a task-local image guard.

## Tests

`AC-EXECUTORS-TASK-GIT-ISOLATION-001.1` through `.4` map to process isolation
unit/integration tests and standalone lifecycle tests. `AC-EXECUTORS-TASK-GIT-
ISOLATION-002.1` through `.4` map to helper-unavailable, malformed projection,
Docker conflict, clone-inside, and existing worktree resolver tests.

## Work orders

- [ ] [Task 01: Define the isolation contract](task-01-isolation-contract.md)
- [ ] [Task 02: Enforce standalone process isolation](task-02-standalone-process-isolation.md)
- [ ] [Task 03: Enforce Docker mount isolation](task-03-docker-mount-isolation.md)
- [ ] [Task 04: Validate rollout compatibility](task-04-rollout-compatibility.md)

## Verification results

Pending.

## Risks

- Linux user-namespace support and helper packaging vary by host.
- A launch path omitted from the wrapper would bypass the security boundary.
