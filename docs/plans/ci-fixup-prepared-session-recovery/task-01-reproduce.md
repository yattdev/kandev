---
id: 01-reproduce
title: Reproduce current-main failures
status: done
wave: 1
depends_on: []
plan: plan.md
spec: ../../specs/tasks/requirements/additional-session-workspace-reuse.md
---

# Reproduce current-main failures

- Intent: execute the corresponding numbered step in the approved plan.
- Acceptance: approved plan AC1 through AC7 as applicable; exact checkout/route ownership, zero-turn guards, no duplicate sessions and first turns.
- Dependencies: None.
- Parallelism: sequential; MAIN is the sole code writer.
- Files likely touched: executor/executor_workspace_reuse_pr_base_test.go, workflow_prepared_retry_test.go (paths in internal/orchestrator unless otherwise qualified).
- Inputs: plan.md source audit and acceptance, canonical 33824242c/dbfd1e793/7bd1b88a9; current main e8c1761c.
- Output contract: changed files, exact commands/results, failure evidence, risks and status update.
- Verification: task-defined targeted Go tests with -trimpath -tags fts5, concurrency with -race, changed-code lint and docs validation. Exact selectors are in plan.md section 8. Task 05 requires two distinct Kandev Review and QA final-candidate verdicts; merge/release/runtime acceptance remain later delivery gates.

## Results

RED on upstream e8c1761cb5b7006b905c0aa995925372ce8664e1: `cd apps/backend && go test -trimpath -tags fts5 ./internal/orchestrator/executor -run TestWorkspaceReusePRBaseLaunch -count=1` failed with expected upstream/main, actual main. `cd apps/backend && go test -trimpath -tags fts5 ./internal/orchestrator -run TestPreparedWorkflowReplay -count=1` failed with expected prepared-destination, actual newly allocated UUID. This is executable source behavior, not released-image acceptance. Current main rejects an empty PR checkout head; that obsolete association was not admitted or used to weaken current provider checks.

Temporary databases use repository-provided fixtures in Go test temporary directories. Cleanup is t.Cleanup-owned. No credentials or live data were used.
