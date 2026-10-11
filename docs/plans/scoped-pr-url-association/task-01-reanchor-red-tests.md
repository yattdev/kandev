---
id: "01-reanchor-red-tests"
title: "Re-anchor and red tests"
status: done
wave: 1
depends_on: []
plan: "plan.md"
spec: "../../specs/integrations/requirements/github-scoped-pr-url-association.md"
---

# Task 01: Re-anchor and red tests

- Acceptance: Protocol, handler and fork coordinator failures recorded before fixes.
- Inputs: approved platform plan, plan.md sections 2, 6, 7 and 8, integration requirement/system-design pair, scoped backend/GitHub guidance.
- Files likely touched: `docs/specs/integrations/requirements/github-scoped-pr-url-association.md`, `docs/specs/integrations/system-design/github-scoped-pr-url-association.md`, `docs/decisions/2026-10-11-scoped-pr-url-association.md`, `apps/backend/internal/mcp/server/task_scoped_pr_url_test.go`, `apps/backend/internal/backendapp/task_scoped_pr_url_test.go`.
- Dependencies: None.
- Parallelism: sequential. Only independent Review and QA side sessions after committed candidate are authorized.
- Verification (from repo root): `cd apps/backend && go test -trimpath ./internal/mcp/server ./internal/mcp/handlers ./internal/backendapp -run 'Test.*ScopedPRURL' -count=1`.
- Risks: persisted authority and concurrent scope changes must fail closed; use disposable repository fixtures only. No live deployment/provider mutations.
- Output contract: record exact files, commands, results, candidate SHA, limitations and task/plan status.

## Results

MAIN focused suite passed:
`cd apps/backend && go test -trimpath -race ./internal/mcp/handlers ./internal/mcp/server ./internal/backendapp ./internal/github -run 'Test.*(ScopedPRURL|TaskChange|AssociateExistingPR|ListTaskPRsByPRNumber)' -count=1` (four packages).

Shared writer regressions passed:
`cd apps/backend && go test -trimpath -race ./internal/github -run 'Test.*(CreateTaskPR|ReplaceTaskPR|RestoreTaskPR|ReconcileTaskPR)' -count=1`.

`go run ./cmd/sqlguard ./internal` and `go test -trimpath -race ./internal/persistence/storeconformance -count=1` passed from apps/backend. Changed-code golangci-lint passed (0 issues).

Documentation catalog/spec/public-doc validation passed; public-doc tests passed 164/164. Repository fixtures only, test-owned temporary DBs removed by cleanup. No provider or deployment writes. `PROMPT.md` remains local-only.

PostgreSQL concurrency remains UNVERIFIED pending a task-owned DSN. Independent Review/QA and AC14 post-merge release evidence are pending; no skipped check is recorded as passed.
