---
status: current
system: integrations
requirements:
  - REQ-INTEGRATIONS-SCOPED-PR-URL-001
created: 2026-10-11
owners:
  - kandev
---

# Scoped GitHub PR URL Association Design

## Purpose and boundaries

Extend `manage_task_change_request_kandev` instead of restoring the old overlay
tool. Task MCP derives caller/session identity; the backend coordinator resolves
the child's source attachment and authenticated user. Core GitHub routing owns
the provider read. The plugin Host evidence API does not authorize writes.

## Requirement mapping

| Acceptance | Owner |
| --- | --- |
| .1, .2 | MCP schema, handler and trusted principal |
| .3, .4 | GitHub provider validation |
| .5, .6 | Store transaction admission and collision fencing |
| .7, .8 | Shared outcome writers and postcommit readback |

## Admission and provider resolution

Schema keeps its portable root object. Handler cross-field checks require URL
and expected head together, only for GitHub link. Persisted parent/session checks
precede provider access. The source repository remains the task's attached ID;
upstream owner/name/number come from the strict URL and provider response.
Credential routing and workspace repository policies remain the existing ones.
The fresh provider observation is revision evidence, not a GitHub-side lock.

## Persistence

Association insert/restore and ownership reconciliation share a database mutation
lock: SQLite reserves its single writer before reading; PostgreSQL uses a
transaction-scoped advisory lock. This serialization adds no uniqueness schema.
PostgreSQL locks parent, child, session, attachment and source rows with
`FOR SHARE`; the same-slot PR row uses `FOR UPDATE`. SQLite's writer reservation
fences those mutations for the transaction. Recheck live state, workspace,
parent relationship, session policy and source identity after provider fetch.

The transaction rejects same-slot URL/stored-head conflicts and active competing
links, then reuses the established outcome-field insert/restore writers.
Manual multi-task links remain allowed by their existing entry points.
Only a changed committed row emits the existing association event and comparison
projection. Exact persisted row and active links are read back for the response.
Failure to establish final state never becomes success.

## Compatibility and validation

The local merged validator fix is regression evidence: association can admit a
verified merged deliverable while contribution editing stays open-only.
Protocol, handler/coordinator, fake-provider/real-store, scope, restoration,
concurrency and PostgreSQL checks live alongside their owning Go packages.
No UI component, Docker image, plugin API or live runtime changes.

## Delivery

Source acceptance precedes independent Review and QA on an immutable commit.
Overlay retirement additionally requires merged upstream SHA, release/artifact
containment and isolated binary acceptance. Source tests cannot waive that gate.
