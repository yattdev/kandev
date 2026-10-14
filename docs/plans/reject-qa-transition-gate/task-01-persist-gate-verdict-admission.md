---
id: "01-persist-gate-verdict-admission"
title: "Persist and enforce completion gate admission"
status: in_progress
wave: 1
depends_on: []
plan: "plan.md"
requirements:
  - REQ-TASKS-WORKFLOW-EXPLICIT-COMPLETION-SIGNAL-001
  - REQ-TASKS-WORKFLOW-EXPLICIT-COMPLETION-SIGNAL-004
acceptance_criteria:
  - AC-TASKS-WORKFLOW-EXPLICIT-COMPLETION-SIGNAL-001.3
  - AC-TASKS-WORKFLOW-EXPLICIT-COMPLETION-SIGNAL-004.1
  - AC-TASKS-WORKFLOW-EXPLICIT-COMPLETION-SIGNAL-004.2
  - AC-TASKS-WORKFLOW-EXPLICIT-COMPLETION-SIGNAL-004.3
  - AC-TASKS-WORKFLOW-EXPLICIT-COMPLETION-SIGNAL-004.4
  - AC-TASKS-WORKFLOW-EXPLICIT-COMPLETION-SIGNAL-004.5
  - AC-TASKS-WORKFLOW-EXPLICIT-COMPLETION-SIGNAL-004.6
system_design:
  - ../../specs/tasks/system-design/workflow-explicit-completion-signal.md
---

# Task 01: persist and enforce completion gate admission

## Outcome and scope

Implement the [plan](plan.md) and its linked requirement/design as one backend
slice: durable gate identity across rename/import/export/restart, calling-turn
transport, first-wins claim, cancellation admission and atomic generation
validation at both transition commits. Preserve existing source receipts,
handoff payloads, pending moves and non-gate behavior. Integrate current main
additively with an ordinary merge, preserving its runnable-move applicability,
entry-prompt delivery guards, native prompt protocol and start-step edit intent.

## Acceptance

1. BLOCKED, FAILED, absent, blocker-bearing or stale Review/QA verdicts cannot
   advance; current blocker-free explicit PASS can in both funnels.
2. Same-step retries and real away/return changes between preflight and commit
   reject the old signal atomically without consuming it or writing destination
   state; duplicate completion writers commit at most once.
3. Policy projections, rename/replay, successful handoffs, source receipts,
   pending queues and upstream delivery/edit-intent behavior retain coverage.

## Files and boundaries

- MCP handler/server completion transport and applicability response.
- Workflow models, repository, controller, templates, import/export and events.
- Orchestrator legacy/engine completion and prompt dispatch.
- Task repository turn stamps, durable claims and shared admission transaction.
- Requirement/design and public workflow reference/how-to guidance.

No UI, provider credentials, live runtime migration/restart, deployment,
protected-lane movement or Human acceptance is authorized by this work order.
No runtime fixture is shared with another task or a Human test instance.

## Verification

From the repository root, run:

```bash
(cd apps/backend && go test -trimpath -race ./internal/orchestrator ./internal/mcp/handlers ./internal/mcp/server ./internal/workflow/... ./internal/task/repository/sqlite -run 'TestCompletion|TestPersistedGate|TestStepComplete|TestHandleStepComplete|TestGate|TestPendingMove|TestApplyPendingMove|Test.*StartStep|Test.*Reorder|Test.*UpdateIntent|Test.*EntryPrompt|Test.*Delivery' -count=1)
(cd apps/backend && go test -trimpath -race ./internal/persistence/storeconformance -count=1)
(cd apps/backend && go run ./cmd/sqlguard ./internal)
node --test scripts/validate-public-docs.test.mjs
node scripts/validate-public-docs.mjs
python3 scripts/list-docs.py validate
python3 scripts/lint-spec-files.py --all
git diff --check
```

Set `KANDEV_TEST_POSTGRES_DSN` to an independently owned disposable fixture for
repository and store-conformance commands, and confirm relevant PostgreSQL
tests run without skips. Also run full affected backend packages and normal
commit hooks; run exact-head PR documentation coverage with this changed work
order and sibling plan. Backend protocol/persistence changes have no rendered
UI, so browser screenshots do not prove these acceptance criteria.

## Results

Earlier candidate `2d3b3e59` completed the six acceptance points and passed
independent Review and distinct QA, including real PostgreSQL concurrency.
Current-main integration and its exact-successor evidence remain in progress.
The earlier candidate remains historical evidence; it is not a current-head
pass or Human acceptance. Permanent regressions include
`completion_commit_generation_test.go`, `completion_admission_test.go`,
`completion_admission_postgres_test.go`, `completion_gate_subscriber_test.go`
and the gate policy/turn-generation contract tests.

Integration regression evidence: a new real-PostgreSQL server-observed lock
probe failed for both standard and CAS writes when the inherited hierarchy
lock preceded session admission (SQLSTATE 55P03). Moving admission locking
before hierarchy locking passed the regression and atomic admission cases
under `-race` on the task-owned PostgreSQL fixture. This preserves the lock
ordering invariant while retaining upstream hierarchy serialization.
