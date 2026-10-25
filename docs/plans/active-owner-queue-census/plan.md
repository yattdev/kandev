---
created: 2026-10-25
status: in_progress
requirements:
  - REQ-TASKS-ACTIVE-QUEUE-CENSUS-001
system_design:
  - ../../specs/tasks/system-design/active-owner-queue-census.md
legacy_specs: []
---

# Implementation Plan: Active-owner read-only queue census

## Overview

Deliver one independently useful observational primitive on canonical main
`cc687bdce96bc22fb8a9ca87abe571b8309d3125`. Keep the complete frozen `fea9`
source/private archive preserved separately. This package does not port that
candidate, depend on its old task, or authorize publication.
Root admitted this bounded source slice on 2026-10-25; publication remains separate.

The task system owns the session-bound FIFO observation. The existing
authentication, stream execution and task access boundaries remain authoritative.
The [requirement](../../specs/tasks/requirements/active-owner-queue-census.md) and
[design](../../specs/tasks/system-design/active-owner-queue-census.md) define the
vertical contract. Issue #4758 owns the upstream request; Root's source-slice
approval now covers this implementation, while maintainer fit remains open.

## Scope

### In scope

- One empty-argument task/Office MCP tool, strict execution authorization,
  pure bounded repository projection, typed response and focused public reference.
- Memory, SQLite and PostgreSQL parity; synthetic end-to-end MCP consumer tests.

### Out of scope

- Claims/removal, all-writer mutation revisions, coalescing, routine wake,
  terminal/child readers, new trust stores, flags, UI, real queues or runtimes.
- Old stack publication, closed PR revival, duplicate issue/PR, cleanup,
  credential, security-policy or deployment changes.

## Technical approach

Implement one sequential vertical work order. Add focused census files in
`internal/orchestrator/messagequeue`, `internal/orchestrator`, MCP handlers and
server. Extend the existing repository contract, add the WS action in
`pkg/websocket/actions.go`, and add an explicit task/Office registration group in
`internal/mcp/server/server.go`. Use a narrow capability on the existing
`sessionLauncher` rather than a new engine or broad constructor change.

`MCPExecutionContext`, principal and current owner access authorize the exact
RUNNING execution before any queue dependency is consulted. A pure reader uses
`r.ro`; existing Snapshot/identity-resolution methods are forbidden for this
path because they write state. Snapshot consistency plus final authority
revalidation fence the observation. The response digest is content-addressed;
it deliberately supplies no chronological revision or mutation CAS guarantee.

| Provider | Boundary | Verification | Unsupported fallback |
| --- | --- | --- | --- |
| Memory | Existing mutex and canonical identity callback | Complete state equality, race tests | Unavailable, no synthetic identity repair |
| SQLite | Existing deferred reader, read-only snapshot | Separate writer held; no SQL writes; exact bytes | Unavailable, never writer fallback |
| PostgreSQL | Existing reader, read-only repeatable-read transaction | Real multi-connection gated parity test | Unavailable; missing DSN remains untested |
| Task/Office MCP | Current stream/principal and exact execution | HTTP dispatcher plus raw-action denial tests | Other surfaces deny; no broader target |

## Tests

Focused coverage lives beside the changed source; final verification results
are recorded after the committed candidate is checked.

| Acceptance criteria | Proposed test and file |
| --- | --- |
| .1, .4, .7 | `TestActiveQueueCensusProjection`, `TestActiveQueueCensusRevisionAndLimits`, `TestActiveQueueCensusMemoryOrdersByPositionAndID`, and SQL empty/visible cases in `messagequeue/census_read*_test.go` |
| .2, .3 | Orchestrator missing/stale/terminal execution tests, handler raw-action/context and trusted-dispatcher tests, and server surface-registration tests |
| .4 | `TestActiveQueueCensusRejectsConcurrentCapacityChange` and `TestActiveQueueCensusRejectsAuthorityChangeAcrossSnapshot` |
| .5 | Visible/examined row, content/metadata size and hidden-row identifier failures in `messagequeue/census_read_test.go`; strict arguments in handler/server tests |
| .6, .7 | `TestActiveQueueCensusSQLiteReadOnly`, `TestActiveQueueCensusPostgres`, and trusted dispatcher through the authority-aware memory provider |
| .8 | Public MCP reference states snapshot and acknowledgement limits |

## E2E tests

Use process-local MCP registration and raw dispatcher tests, including a trusted
dispatcher through real handlers, orchestrator and authority-aware memory queue.
SQLite exercises the read-only SQL projection; PostgreSQL requires a disposable
test DSN. This is synthetic consumer evidence, not deployed-runtime or end-user
acceptance. No Playwright test is needed for a tool-only contract with no
rendered UI.

## Work orders

- [ ] [Task 01: Read-only active-owner census](task-01-read-only-census.md) (in progress)

## Verification results

Design validation is recorded in the native task plan. Focused local tests and
lint have passed during implementation. Final exact-head Review/QA and the
disposable PostgreSQL check remain pending; no old reports confer a pass.

## Risks

- Snapshot and status helpers currently mutate; accidental reuse breaks .6.
- Metadata needed for visibility can defeat bounded reads unless preflighted.
- Independent authority stores do not provide atomic cross-store revocation.
- Hashes reveal equality and byte length; owner-only authorization is essential.
- Root approval does not establish upstream maintainer acceptance of issue #4758.
