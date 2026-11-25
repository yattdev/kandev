---
status: draft
system: tasks
created: 2026-11-25
owners:
  - kandev
---

# Coordinator Task Lifecycle Lookup

## Why

A Coordinator needs authoritative metadata for a known task ID when that task
is absent from its board list. Neither an omitted row nor a failed plan read
establishes that the task was deleted.

## What

The guarded Coordinator can read one task's lifecycle metadata in its current
workspace without reading task documents or changing the board. The result
preserves uncertainty about missing rows and keeps archive retention intact.

## Requirements

### REQ-TASKS-COORDINATOR-LIFECYCLE-001: Bound read authority

- **AC-TASKS-COORDINATOR-LIFECYCLE-001.1:** A lookup shall derive its caller
  task, session and workspace from authenticated transport and current launch
  metadata. Payloads shall supply only the target task UUID. Missing, ambiguous,
  stale or non-Coordinator caller proofs shall deny before target lookup.
- **AC-TASKS-COORDINATOR-LIFECYCLE-001.2:** A target in another workspace,
  including one owned by the same user, shall produce the same result as an
  absent target. The response shall reveal no foreign workspace or lifecycle.

### REQ-TASKS-COORDINATOR-LIFECYCLE-002: Truthful bounded metadata

- **AC-TASKS-COORDINATOR-LIFECYCLE-002.1:** An eligible lookup shall return
  `active` when `archived_at` is null and `archived` when it is set, together
  with task ID, current workspace/workflow/step IDs, state, update timestamp
  and archive timestamp. `active` describes archive status, not a running agent.
- **AC-TASKS-COORDINATOR-LIFECYCLE-002.2:** An absent or inaccessible target
  shall return `TASK_NOT_FOUND_OR_INACCESSIBLE` with deletion evidence
  `unproven`. Without an authoritative workspace-owned tombstone, the reader
  shall never report `deleted`, `moved`, or an archive timestamp for that ID.
- **AC-TASKS-COORDINATOR-LIFECYCLE-002.3:** The lookup shall report plan
  presence as `present` or `absent`; `absent` shall carry `PLAN_NOT_FOUND`.
  It shall return no plan body, title, session list, path, customer content,
  environment, credential, queue, or launch payload. Response size is at most
  4 KiB. Storage failures shall remain typed failures, not missing results.

### REQ-TASKS-COORDINATOR-LIFECYCLE-003: Meaningful plan read failures

- **AC-TASKS-COORDINATOR-LIFECYCLE-003.1:** The MCP plan reader shall preserve
  missing/inaccessible task errors as `NOT_FOUND`, known permission denials as
  `FORBIDDEN`, and an authorized planless task as `NOT_FOUND` with reason
  `PLAN_NOT_FOUND`. Foreign/missing target errors shall share a message and
  reason. Genuine storage errors shall remain `INTERNAL_ERROR`.
- **AC-TASKS-COORDINATOR-LIFECYCLE-003.2:** Plan error repair shall preserve
  existing caller authorization, bounded reads, CAS versions, revision data,
  archived plans and browser no-plan success behavior. It shall not add task
  metadata to a denied document read or imply permission from retained content.

### REQ-TASKS-COORDINATOR-LIFECYCLE-004: Preservation

- **AC-TASKS-COORDINATOR-LIFECYCLE-004.1:** The read shall not mutate tasks,
  plans, sessions, leases, lanes, queues, routines or automation state, start a
  provider, perform filesystem probes, or install/restart a service. Its fixed
  audit receipt may identify caller/target IDs and outcome but no content.

## Scenarios

Synthetic conformance covers active, archived, missing, planless and present-plan
targets; malformed UUID; unauthorized/expired/ambiguous/mismatched callers;
foreign-owner and same-owner foreign workspaces; storage failure; preserved
archive/plan bytes; and absence without a tombstone. A workflow move inside the
same workspace returns current placement, without inferring historical movement.

## Out of scope

Board census, reconciliation actions, deletion tombstones, historical placement,
new Coordinator identities, cross-workspace grants and live activation are
excluded. The legacy registered-repository Coordinator is not repinned to the
new native attended Coordinator surface.

## Delivery

[Implementation plan](../../../plans/coordinator-task-lifecycle/plan.md).
