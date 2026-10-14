---
created: 2026-09-29
status: in_progress
requirements:
  - REQ-TASKS-WORKFLOW-EXPLICIT-COMPLETION-SIGNAL-001
  - REQ-TASKS-WORKFLOW-EXPLICIT-COMPLETION-SIGNAL-004
system_design:
  - ../../specs/tasks/system-design/workflow-explicit-completion-signal.md
legacy_specs: []
---

# Implementation plan: durable completion gate admission

## Outcome and scope

Persist Review and QA gate policy and bind explicit PASS admission to the
current task, session, step and immutable turn generation. Validate this proof
inside the automatic transition transaction in both orchestration paths.
Preserve manual moves, existing handoffs, source receipts and pending arrivals.

The approved implementation is complete on the earlier candidate. This delivery
record tracks integration with current main and the affected revalidation;
earlier Review and QA results do not authorize a later revision.

## Work orders

- [Task 01: persist and enforce gate verdict admission](task-01-persist-gate-verdict-admission.md)

## Approach and risks

Follow the [system design](../../specs/tasks/system-design/workflow-explicit-completion-signal.md) and
[requirements](../../specs/tasks/requirements/workflow-explicit-completion-signal.md).
Use one vertical work order for policy persistence/projections, immutable turn
transport, durable claims, admission and regression evidence. Retain both
legacy and engine successful transitions. Session turn-write locks precede
workspace/step/task locks; the repository revalidates durable signal metadata
and ledger generation in the write transaction.

Current-main integration must retain workflow start-step update intent,
reordering, native prompt delivery, pending entry-prompt gating and truthful
completion applicability responses. Recheck auto-merged paths as well as
textual conflicts. No live runtime, deployment, Human acceptance or merge into
main is part of this work package.

## Verification and delivery

The work order owns exact focused/race, SQLite/PostgreSQL, documentation and
normal hook checks. Obtain independent Review PASS then distinct QA PASS on
one final committed revision. Record tested, published and Human-accepted
revisions independently. Publication requires the repository's contribution
process and applicable maintainer discussion; its absence does not stop local
integration. Historical candidate and remote history remain intact.
