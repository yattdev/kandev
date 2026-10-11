---
status: active
system: tasks
created: 2026-10-11
updated: 2026-10-11
owners:
  - kandev
---

# Task-local Coordinator instruction requirements

## Overview

Coordinator may issue a task-specific instruction document without changing a
repository-owned `AGENTS.md`. The document applies only to its bound task
environment and must be reloaded before each governed prompt admission.

## Requirements

### REQ-TASKS-LOCAL-INSTRUCTIONS-001: Bound protected instruction documents

**Intent:** A task receives current Coordinator direction without granting an
agent authority to alter its own instruction source.

#### Acceptance criteria

- **AC-TASKS-LOCAL-INSTRUCTIONS-001.1:** The Host shall bind content, digest,
  version, task, workspace, task environment, and ownership generation before
  publishing a task instruction mirror outside every repository checkout.
- **AC-TASKS-LOCAL-INSTRUCTIONS-001.2:** Publishing shall preserve all
  repository-tracked `AGENTS.md` bytes. Repository sources shall be reported
  before the task-local addendum in the prompt context.
- **AC-TASKS-LOCAL-INSTRUCTIONS-001.3:** Governed admission shall reject a
  missing, stale, tampered, foreign, generation-mismatched, or unreadable
  binding or mirror before provider dispatch.
- **AC-TASKS-LOCAL-INSTRUCTIONS-001.4:** A chmod-only or same-principal local
  projection shall not qualify as an executor guard. Unsupported executors
  shall reject governed admission with a recoverable guard reason.
- **AC-TASKS-LOCAL-INSTRUCTIONS-001.5:** The Host shall resolve parent and
  report-to identifiers from authoritative task lineage at issuance and
  rebind. A missing relation requires a scoped Coordinator decision; caller
  text cannot establish either identifier.
- **AC-TASKS-LOCAL-INSTRUCTIONS-001.6:** Done tasks and policy-protected
  ToDeploy tasks shall not receive, update, or use a governed binding.

## System design

[Task-local Coordinator instructions](../system-design/task-local-instructions.md)

## Related decisions

[Task-owned instruction control roots](../../../decisions/2026-10-11-task-instruction-control-root.md)
