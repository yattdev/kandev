---
id: 04-startup-recovery
title: Reconcile prepared routes on startup
status: done
wave: 1
depends_on: ['03-exact-rearm']
plan: plan.md
spec: ../../specs/tasks/requirements/additional-session-workspace-reuse.md
---

# Reconcile prepared routes on startup

- Intent: execute the corresponding numbered step in the approved plan.
- Acceptance: approved plan AC1 through AC7 as applicable; exact checkout/route ownership, zero-turn guards, no duplicate sessions and first turns.
- Dependencies: 03-exact-rearm.
- Parallelism: sequential; MAIN is the sole code writer.
- Files likely touched: service.go, workflow_prepared_retry.go, workflow_prepared_retry_test.go (paths in internal/orchestrator unless otherwise qualified).
- Inputs: plan.md source audit and acceptance, canonical 33824242c/dbfd1e793/7bd1b88a9; current main e8c1761c.
- Output contract: changed files, exact commands/results, failure evidence, risks and status update.
- Verification: task-defined targeted Go tests with -trimpath -tags fts5, concurrency with -race, changed-code lint and docs validation. Exact selectors are in plan.md section 8. Task 05 requires two distinct Kandev Review and QA final-candidate verdicts; merge/release/runtime acceptance remain later delivery gates.

## Results

GREEN: `cd apps/backend && go test -trimpath -tags fts5 ./internal/orchestrator -run 'Test.*(PreparedWorkflow|WorkflowSessionTarget|RestartReconciliation|PrepareWorkflowStepSession|ProfileSessionPolicy)' -count=1 -timeout=90s` (0.499s). Includes first-turn idempotence for CREATED/FAILED, parallel startup, changed step during attach, no-turn refusal matrix, ordinary kept-route control, and joined shutdown cancellation. Final GREEN: `cd apps/backend && go test -trimpath -race -tags fts5 ./internal/orchestrator ./internal/orchestrator/executor ./internal/task/repository/sqlite -run 'Test.*(PreparedWorkflow|WorkflowSessionRoute|WorkspaceReusePRBase)' -count=1 -timeout=120s` passed (1.895s, 1.126s, 2.456s). PostgreSQL env-gated test remained unexecuted.

Temporary databases use repository-provided fixtures in Go test temporary directories. Cleanup is t.Cleanup-owned. No credentials or live data were used.
