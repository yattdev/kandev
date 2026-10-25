---
id: "01-read-only-census"
title: "Read-only active-owner queue census"
status: in_progress
wave: 1
depends_on: []
plan: "plan.md"
requirements:
  - REQ-TASKS-ACTIVE-QUEUE-CENSUS-001
acceptance_criteria:
  - AC-TASKS-ACTIVE-QUEUE-CENSUS-001.1
  - AC-TASKS-ACTIVE-QUEUE-CENSUS-001.2
  - AC-TASKS-ACTIVE-QUEUE-CENSUS-001.3
  - AC-TASKS-ACTIVE-QUEUE-CENSUS-001.4
  - AC-TASKS-ACTIVE-QUEUE-CENSUS-001.5
  - AC-TASKS-ACTIVE-QUEUE-CENSUS-001.6
  - AC-TASKS-ACTIVE-QUEUE-CENSUS-001.7
  - AC-TASKS-ACTIVE-QUEUE-CENSUS-001.8
system_design:
  - ../../specs/tasks/system-design/active-owner-queue-census.md
---

# Task 01: Read-only active-owner queue census

## Summary

Root approved this exact slice and the implementation request started work.
Implement the proposed single observational tool on freshly reconciled
main. Deliver the pure projection, current-execution guards, strict public
consumer contract, focused tests and public reference together.

## In scope

- All criteria in the linked requirement and test matrix in plan.md.
- TDD against synthetic data through existing repositories and MCP dispatcher.
- Preserve exact trusted-context, incarnation, bounded read and no-write rules.

## Out of scope

Old claims/removal/revision-writer stack, live queues, fixture reuse, public
posts/PRs, runtime installation, deployment, security-policy changes and cleanup.

## Acceptance

1. The only new public operation is the empty-object owner-bound read tool;
   authorization failures occur before any queue access and excluded surfaces
   cannot execute a raw action.
2. All three providers return a complete bounded body-free coherent observation
   or explicit error, without any state, policy, reservation or counter writes.
3. The planned consumer and concurrency tests pass; public reference documents
   exact limits and staleness. PostgreSQL evidence is explicitly recorded, or
   its absence remains a delivery limitation requiring resolution.

## Verification

Run from the repository root. The PostgreSQL run uses a newly authorized
disposable database, never the retained shared fixture. A missing DSN means
the gated PostgreSQL test is skipped, not passed.

```bash
(cd apps/backend && go test -trimpath -race ./internal/orchestrator/messagequeue ./internal/orchestrator ./internal/mcp/handlers ./internal/mcp/server -run 'TestActiveQueueCensus' -count=1)
(cd apps/backend && go test -trimpath ./internal/mcp/profile ./internal/mcp/server -count=1)
(cd apps/backend && go run ./cmd/sqlguard ./internal)
python3 scripts/list-docs.py validate
python3 scripts/lint-spec-files.py --all
node scripts/validate-public-docs.mjs
git diff --check
```

Run `bash scripts/lint-go-changed` with the exact changed Go paths from the
repository root. Before marking done, run the same census race command with
`KANDEV_TEST_POSTGRES_DSN` supplied through the authorized environment; record
the real PostgreSQL test result separately. Do not print the DSN.

## Files likely touched

- `apps/backend/internal/orchestrator/messagequeue/repository.go`
- New `census_read.go`, `repository_census_read_memory.go`,
  `repository_census_read_sql.go` and focused tests in
  that package. Existing memory/SQL structures stay in their current files.
- New `apps/backend/internal/orchestrator/message_queue_census.go` and tests.
- New `apps/backend/internal/mcp/handlers/message_queue_census.go` and tests;
  `handlers.go` action registration only.
- New `apps/backend/internal/mcp/server/message_queue_census.go` and consumer
  tests; `server.go` explicit profile group and registration only.
- `apps/backend/pkg/websocket/actions.go`
- `docs/public/automation-and-mcp.md`
- This package and its requirement/design for final status/results.

Before widening this list to identity/authority stores, runtime lifecycle,
queue writers, schema or generic transports, return to Root with the concrete
need. Do not treat the frozen source stack as an approved dependency list.

## Dependencies

Root's concrete source-slice approval and explicit implementation request were
received on 2026-10-25. Current canonical main is the implementation base.
Issue #4758 feedback and the predecessor task are separate from this source
slice; the predecessor runtime is not a dependency.

## Risks

See plan.md. Snapshot counters, SQLite writer admission and PostgreSQL isolation
are explicit regression boundaries. No automatic fallback to full-row status.

## Parallelism

`sequential`

## Inputs

- [Requirement](../../specs/tasks/requirements/active-owner-queue-census.md)
- [Design](../../specs/tasks/system-design/active-owner-queue-census.md)
- Current trusted execution, principal, exact-pair and read-only pool patterns
  linked from the design. The frozen source is reference material only.

## Results

In progress. No production or permanent test changes were made in the Spec turn.
