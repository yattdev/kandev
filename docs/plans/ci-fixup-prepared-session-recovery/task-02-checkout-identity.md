---
id: 02-checkout-identity
title: Preserve materialized checkout identity
status: done
wave: 1
depends_on: ['01-reproduce']
plan: plan.md
spec: ../../specs/tasks/requirements/additional-session-workspace-reuse.md
---

# Preserve materialized checkout identity

- Intent: execute the corresponding numbered step in the approved plan.
- Acceptance: approved plan AC1 through AC7 as applicable; exact checkout/route ownership, zero-turn guards, no duplicate sessions and first turns.
- Dependencies: 01-reproduce.
- Parallelism: sequential; MAIN is the sole code writer.
- Files likely touched: executor/executor_resume.go, executor/executor_execute.go, executor/executor_workspace_reuse_pr_base.go (paths in internal/orchestrator unless otherwise qualified).
- Inputs: plan.md source audit and acceptance, canonical 33824242c/dbfd1e793/7bd1b88a9; current main e8c1761c.
- Output contract: changed files, exact commands/results, failure evidence, risks and status update.
- Verification: task-defined targeted Go tests with -trimpath -tags fts5, concurrency with -race, changed-code lint and docs validation. Exact selectors are in plan.md section 8. Task 05 requires two distinct Kandev Review and QA final-candidate verdicts; merge/release/runtime acceptance remain later delivery gates.

## Results

GREEN: `cd apps/backend && go test -trimpath -tags fts5 ./internal/orchestrator/executor -run 'Test.*(WorkspaceReuse|ReuseEnvironment|PRBase|PrepareResumeRepositorySettings)' -count=1 -timeout=90s` (4.845s). `cd apps/backend && go test -trimpath -tags fts5 ./internal/worktree -run 'Test.*(ReuseRequired|QualifiedPRBase)' -count=1 -timeout=90s` (5.299s). Preserves live comparison and fresh materialization fields, all repeated-repository slots, and existing fail-closed integrity tests.

Temporary databases use repository-provided fixtures in Go test temporary directories. Cleanup is t.Cleanup-owned. No credentials or live data were used.

After the resume credential-scope refinement, the same executor selector passed again (3.750s), including the new fork credential-identity regression. Preparation uses normalized copies; credential consumers retain verified contribution metadata.
