---
created: 2026-09-29
status: done
requirements:
  - REQ-PLUGINS-EXACT-HOST-001
system_design:
  - ../../specs/plugins/system-design/exact-host-boundary.md
legacy_specs: []
---

# Implementation Plan: Exact Plugin Host Boundary

## Overview

Add an additive, workspace-bound plugin Host contract for exact inventory and
decision evidence, then use that evidence for one bounded and auditable task
description-marker update. The vertical slice preserves legacy v1 behavior and
keeps Coordinator policy and state plugin-owned.

## Scope

### In scope

- Connection-derived capability context and approval-bound exact reads.
- Snapshot-bound workspace, workflow, task, session, relation, and pending
  transition evidence with Host receipts and resource versions.
- Server-owned exact-task grant, atomic marker update, idempotency, audit, and
  durable event recovery.
- Exact session progress and sanitized message reads bound to session
  generation.

### Out of scope

- Coordinator policy, scheduling, persistent state, prompts, reports, and UI.
- Global MCP/private REST/database access from plugins.
- Any exact task write other than the description marker.
- Exact writes on storage backends without proved atomic support.
- Live credentials, external provider actions, deployment, merge, or release.

## Technical approach

The additive exact RPCs use the generated plugin Host protocol and SDK.
Generic plugin Host handlers derive installation identity from the connection
and check manifest plus workspace approval before invoking exact query and
command adapters. Read snapshots bind bounded pagination to workspace,
filters, approval revision, and snapshot version. Decision evidence includes
complete relation endpoints and pending transition state. The exact task
command consumes a server-minted grant and validates every precondition in the
task transaction before writing the marker, audit record, and durable event.
Session and message projections use a durable generation-bound snapshot and
return sanitized fields only.

## Tests

- Plugin Host tests cover approval context, exact pages, decision evidence,
  cross-workspace denial, grant lifecycle, command preconditions, and restart
  recovery.
- Backend composition tests exercise public Host command and sanitized-message
  paths after service reconstruction.
- SDK wire tests prove exact requests and receipts cross the generated gRPC
  boundary.
- SQLite repository and task-service tests cover snapshots and outbox recovery.

## Work orders

- [x] [Task 01: Exact Host contract and bounded task marker](task-01-exact-host-contract.md)

## Verification results

At Host source-contract head 8ee2296820054736df6e8ad98ca6bdc09688c49f,
independent two-workspace QA exhausted inventory pages for two disposable
workspaces, verified decision evidence, applied one marker in workspace A,
reopened the authority, read back its audit/resource version, and confirmed
workspace B remained unchanged. Focused normal and race checks passed for the
plugin lifecycle, competing grants, exact command/outbox, snapshots, and SDK
wire paths. Full normal internal/plugins, internal/backendapp,
internal/task/repository/sqlite, and pkg/pluginsdk packages passed. Proto
generation, go vet, changed-package lint, and git diff --check passed.

## Risks

- A backend without equivalent atomic semantics must keep exact writes
  unavailable rather than emulate the transaction.
- A committed event with unresolved delivery must remain pending or unknown
  after restart; it must not be inferred as delivered.
