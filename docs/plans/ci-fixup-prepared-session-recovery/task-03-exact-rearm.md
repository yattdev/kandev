---
id: 03-exact-rearm
title: Recover the exact prepared destination
status: in_progress
wave: 1
depends_on: ['02-checkout-identity']
plan: plan.md
spec: ../../specs/tasks/requirements/additional-session-workspace-reuse.md
---

# Recover the exact prepared destination

- Intent: execute the corresponding numbered step in the approved plan.
- Acceptance: approved plan AC1 through AC7 as applicable; exact checkout/route ownership, zero-turn guards, no duplicate sessions and first turns.
- Dependencies: 02-checkout-identity.
- Parallelism: sequential; MAIN is the sole code writer.
- Files likely touched: workflow_prepared_retry.go, event_handlers_workflow.go, ../task/repository/sqlite/workflow_prepared_rearm.go (paths in internal/orchestrator unless otherwise qualified).
- Inputs: plan.md source audit and acceptance, canonical 33824242c/dbfd1e793/7bd1b88a9; current main e8c1761c.
- Output contract: changed files, exact commands/results, failure evidence, risks and status update.
- Verification: task-defined targeted Go tests with -trimpath -tags fts5, concurrency with -race, changed-code lint and docs validation. Exact selectors are in plan.md section 8. Task 05 requires two distinct Kandev Review and QA final-candidate verdicts; merge/release/runtime acceptance remain later delivery gates.

## Results

GREEN: `cd apps/backend && go test -trimpath -tags fts5 ./internal/task/repository/sqlite -run 'Test.*(PreparedWorkflow|WorkflowSessionRoute)' -count=1 -timeout=90s` (1.065s), SQLite atomic state pair, rollback, turn/generation/submission/block/route/transition rejection. Exact source/route/step checks also protect promotion after attach. PostgreSQL test is env-gated and not yet executed; parent-approved fixture requested. No schema change, live DB or overlay operation.

Temporary databases use repository-provided fixtures in Go test temporary directories. Cleanup is t.Cleanup-owned. No credentials or live data were used.

Review correction: both blocking advisory/task orders were superseded. Recovery takes the managed task barrier and uses nonblocking exact destination turn/delivery advisory acquisition, preserving every no-turn/runtime/entry guard and rolling back unchanged on contention. PostgreSQL RED/GREEN includes both held advisory boundaries, the runtime lock-footprint and actual competing state/executor, turn and delivery methods; seven PG top-level tests passed -race -count=3 (40.883s).
