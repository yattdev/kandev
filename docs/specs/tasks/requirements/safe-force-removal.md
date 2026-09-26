---
status: draft
system: tasks
created: 2026-09-26
owners:
  - kandev
---

# Safe Force Removal Requirements

## Overview

An authorized operator needs a way to remove one task card when ordinary
deletion cannot inspect its workspace. The operation is a logical quarantine:
it hides only the exact task after proving that new activity is fenced and
existing activity is quiescent. It preserves the task row and all task-owned
evidence for later recovery or separately authorized physical retirement.

## Terminology

- **Force removal:** the single-task logical removal operation defined here.
- **Quarantined task:** a retained task that is hidden from ordinary task
  projections and rejects ordinary mutation.
- **Receipt:** immutable, redacted evidence of an operation or its blocked
  outcome.
- **Unknown:** evidence that could not be verified. Unknown is never clean.

## Requirements

### REQ-TASKS-SAFE-FORCE-REMOVAL-001: Single-task failure escape

**Intent:** Give an operator a safe escape from an unavailable ordinary-delete
inspection without changing ordinary deletion.

#### Acceptance criteria

- **AC-TASKS-SAFE-FORCE-REMOVAL-001.1:** When an authorized single-task delete
  preflight cannot inspect the workspace, the system shall keep ordinary Delete
  unavailable and offer a distinct force-removal preview.
- **AC-TASKS-SAFE-FORCE-REMOVAL-001.2:** A force-removal preview shall identify
  physical workspace state as `UNKNOWN` or `PRESERVED`; it shall not classify
  an inspection error, Git exit 128, missing metadata, or unavailable adapter
  as clean.
- **AC-TASKS-SAFE-FORCE-REMOVAL-001.3:** The operation shall accept one exact
  task only. Bulk, cascade, replacement-transfer, ordinary archive, and
  ordinary delete requests shall not invoke force removal.

### REQ-TASKS-SAFE-FORCE-REMOVAL-002: Exact authorization and confirmation

**Intent:** Ensure that a human or an agent cannot use an identifier supplied
in a request as authority to hide a task.

#### Acceptance criteria

- **AC-TASKS-SAFE-FORCE-REMOVAL-002.1:** Human and agent callers shall use the
  same service authorization and commit path. A foreign or unreadable target
  shall not disclose target existence or inventory.
- **AC-TASKS-SAFE-FORCE-REMOVAL-002.2:** Human commit shall require a fresh
  preview token and entry of the exact target UUID after displaying the task
  title, UUID, disappearance scope, and preserved evidence.
- **AC-TASKS-SAFE-FORCE-REMOVAL-002.3:** Agent commit shall require trusted,
  live caller task and session identity plus a narrowly scoped, expiring,
  single-target grant bound to the target generation. Request-body identity,
  workspace, task, and grant fields shall be treated as data.

### REQ-TASKS-SAFE-FORCE-REMOVAL-003: Durable logical quarantine

**Intent:** Hide the affected card while preserving its evidence and physical
resources unchanged.

#### Acceptance criteria

- **AC-TASKS-SAFE-FORCE-REMOVAL-003.1:** A successful force removal shall hide
  the exact task from ordinary board, search, and task views while retaining
  the task row, its sessions, queues, PR/watch associations, manifests,
  cleanup history, and physical-resource references.
- **AC-TASKS-SAFE-FORCE-REMOVAL-003.2:** A quarantined task shall reject
  unarchive, launch, resume, message, move, worktree, environment, queue,
  dispatch, cleanup, and PR-watch activity.
- **AC-TASKS-SAFE-FORCE-REMOVAL-003.3:** An admin-scoped recovery read shall
  expose an immutable receipt with actor, target and workspace identity,
  generation, predicate result, evidence digest, retained resource identities,
  timestamps, and operation state.

### REQ-TASKS-SAFE-FORCE-REMOVAL-004: Fenced, replay-safe operation

**Intent:** Prevent concurrent work or uncertain external state from hiding or
cleaning the wrong task.

#### Acceptance criteria

- **AC-TASKS-SAFE-FORCE-REMOVAL-004.1:** Before card hiding, the system shall
  atomically claim the exact target and fence admissions and cleanup. A failed
  fence, live execution, running cleaner, shared or foreign resource, or
  unresolved relationship shall leave the card visible with a `BLOCKED` or
  `UNKNOWN` receipt.
- **AC-TASKS-SAFE-FORCE-REMOVAL-004.2:** Commit shall bind target identity,
  target and admission generations, preview digest, and idempotency key. The
  same key and body shall return its stored receipt; a changed body or changed
  state shall conflict or require a fresh preview.
- **AC-TASKS-SAFE-FORCE-REMOVAL-004.3:** The operation shall not invoke
  ordinary delete/archive cleanup, broad filesystem deletion, worktree force
  removal, repository pruning, or physical cleanup. A crash after claim shall
  retain a recoverable operation and shall not imply completion.

## Exclusions

- Deleting either reported reproduction task, bulk force removal, or cascade
  force removal.
- Transferring work to a replacement task or completing the physical retirement
  described by guarded exact-task retirement.
- Automatic PR closure, queue replay, physical repair, or automatic recovery.
