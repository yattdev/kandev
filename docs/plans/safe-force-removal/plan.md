---
created: 2026-09-26
status: in_progress
requirements:
  - REQ-TASKS-SAFE-FORCE-REMOVAL-001
  - REQ-TASKS-SAFE-FORCE-REMOVAL-002
  - REQ-TASKS-SAFE-FORCE-REMOVAL-003
  - REQ-TASKS-SAFE-FORCE-REMOVAL-004
system_design:
  - ../../specs/tasks/system-design/safe-force-removal.md
legacy_specs: []
---

# Implementation Plan: Safe Force Removal

## Overview

Deliver a single-task logical quarantine for tasks whose ordinary delete
preflight cannot inspect a workspace. The operation preserves resources and
evidence; it never extends ordinary delete or physical cleanup.

## Dependency gate

PR #3937 (`feature/add-guarded-exact-ta-age`, head
`9fbfa4caa7dbc53c6c5e782460dbeb277e5b0496`) is open and unmerged as of
2026-09-26. Before shared receipt, predicate, or authorization code changes,
sync its exact merge commit and re-read its contract. Do not fork its paired
preview endpoint. W01 records source-independent contracts now; W02 onward
remain blocked until this gate is satisfied.

The W01 documentation PR is a large architectural proposal from a contributor
without write access to the canonical repository. Its required maintainer
discussion is [issue #3957](https://github.com/kdlbs/kandev/issues/3957). Do
not open the draft PR until that issue has substantive maintainer acceptance.

## Delivery order

1. [W01: Contract and dependency sync](task-01-contract-and-dependency-sync.md)
2. [W02: Durable fence and ledger](task-02-durable-fence-and-ledger.md)
3. [W03: Shared service and HTTP API](task-03-shared-service-http-api.md)
4. [W04: Agent tool surface](task-04-agent-tool-surface.md)
5. [W05: Delete dialog and mobile interaction](task-05-delete-dialog-mobile.md)
6. [W06: Documentation and delivery](task-06-documentation-and-delivery.md)

## UI preview

UI-01: single task with unavailable ordinary preflight.

```text
Desktop
Delete task: <title>
Unable to check workspace changes. [Retry]
[Cancel] [Delete disabled] [Force remove task]

Force removal preview
<title>\n<full UUID>
Workspace: UNKNOWN, preserved
Sessions, queues, PR links, Git state, and recovery evidence remain.
Type UUID: [________________]
[Back] [Force remove task disabled until exact match]

Phone
Task actions -> Delete task
  scrolling body: error, preserved resources, full UUID input
  fixed actions: [Cancel] [Force remove task]
```

The wording is illustrative and must be localized. The force branch is only
for one exact task. Phone uses one scrolling body, safe-area-aware fixed
actions, and 44px targets. UI-01 maps to `AC-TASKS-SAFE-FORCE-REMOVAL-001.1`,
`001.2`, and `002.2`.

## Verification strategy

Each work order owns focused RED/GREEN tests. The final delivery gate includes
focused Go, Vitest, MCP, desktop/mobile Playwright, typecheck, i18n checks,
spec validation, independent review, distinct QA, and exact-head CI. Fixtures
use synthetic IDs and isolated temporary roots; the cited reproduction IDs are
never used.
