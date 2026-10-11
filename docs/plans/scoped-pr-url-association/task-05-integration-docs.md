---
id: "05-integration-docs"
title: "Integration and docs"
status: done
wave: 5
depends_on: ["04"]
plan: "plan.md"
spec: "../../specs/integrations/requirements/github-scoped-pr-url-association.md"
---

# Task 05: Integration and docs

- Acceptance: Real full management path and fault/revocation tests pass; public usage documented.
- Inputs: approved platform plan, plan.md sections 2, 6, 7 and 8, integration requirement/system-design pair, scoped backend/GitHub guidance.
- Files likely touched: `apps/backend/internal/backendapp/task_scoped_pr_url_integration_test.go`, `apps/backend/internal/backendapp/task_change_link_coordinator_test.go`, `docs/public/automation-and-mcp.md`.
- Dependencies: 04.
- Parallelism: sequential. Only independent Review and QA side sessions after committed candidate are authorized.
- Verification (from repo root): `cd apps/backend && go test -trimpath -race ./internal/mcp/handlers ./internal/mcp/server ./internal/backendapp ./internal/github -run 'Test.*(ScopedPRURL|TaskChange|AssociateExistingPR|ListTaskPRsByPRNumber)' -count=1`.
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
