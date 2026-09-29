---
status: current
system: plugins
requirements:
  - REQ-PLUGINS-EXACT-HOST-001
---

# Exact Plugin Host Boundary System Design

## Purpose and boundaries

The plugin system owns the versioned Host API, installation identity,
capability approval, and plugin-facing receipts. Task, workflow, relation, and
session services remain authoritative for their domain state. Coordinator
policy and durable orchestration state remain in the separately released
plugin. The [generic Host boundary decision](../../../decisions/2026-08-31-generic-plugin-host-boundary.md)
provides the broader design input; this design covers the exact read and
description-marker slice implemented here.

## Requirement mapping

| Requirement | Design section |
| --- | --- |
| REQ-PLUGINS-EXACT-HOST-001 | [Host operations](#host-operations), [Write authority](#write-authority), and [Restart evidence](#restart-evidence) |

## Components and responsibilities

- The Host service in apps/backend/proto/kandev/plugin/v1/plugin.proto exposes
  additive exact RPCs and leaves the shipped v1 RPCs intact.
- apps/backend/pkg/pluginsdk maps exact RPCs to typed plugin interfaces and
  preserves workspace, snapshot, version, and receipt fields over gRPC.
- apps/backend/internal/plugins derives installation identity from the
  authenticated connection, checks the installed manifest against the current
  workspace approval, creates exact read receipts, and delegates domain
  queries and commands.
- Task and Office repositories materialize bounded snapshots for task,
  workflow, session, relation, and pending-transition evidence.
- The exact task command service owns task-version compare-and-swap, grant
  consumption, audit identity, and durable task.updated delivery.

## Host operations

GetCapabilityContext reports the Host contract version, context revision,
installed-manifest digest, and workspace-scoped approval state and revision.
Its response is descriptive only. Every exact read or write rechecks current
authorization.

Exact inventory operations are ListWorkspacesExact, ListWorkflowsExact,
ListWorkflowStepsExact, ListTasksExact, GetTaskExact, and ListSessionsExact.
GetSessionExact returns generation-bound session progress.
ListSessionMessagesExact reads the durable sanitized message projection. Lists
use bounded pages over a Host snapshot; opaque cursors bind the workspace,
filters, installation, approval revision, and snapshot version. Every returned
page includes resource versions and a Host read receipt.

ListTaskDecisionEvidenceExact returns relation endpoints and pending
transitions from one decision-evidence snapshot. Its edge, endpoint, task,
session, and queue-generation versions are the evidence consumed by the exact
writer. A page or projection that is expired, incomplete, unknown, stale, or
cross-workspace is rejected and cannot be used to authorize a mutation.

## Write authority

UpdateTaskExact is the only task writer in this slice. It accepts the bounded
description-marker operation, exact task version, decision-evidence snapshot,
pending-transition predicate, capability revision, and idempotency identity.
The Host derives the installation and workspace authority from the live plugin
connection and current approval. The plugin cannot provide a grant identifier
as authority.

The server-owned grant binds the installation, workspace, task, approval
revision, canonical action digest, idempotency identity, expiry, and applicable
session or assignment generation. Grant resolution is single-use. Before
commit, the task command revalidates approval, task version, evidence snapshot,
pending-transition state, provenance, grant, and idempotency digest. A failed
precondition has no task or event effect. A successful change commits one
description update and audit identity and records one task.updated event
through the durable outbox. Same-key, same-digest replay returns the existing
durable result or an explicit pending outcome; a changed digest cannot repeat
the effect.

## Restart evidence

Exact session progress and sanitized messages are materialized against the
session identity, queue incarnation, route generation, and resource version.
The public projection excludes secret tokens and unsanitized message payloads.
The command receipt distinguishes durable completion from unresolved delivery.
After reopening the Host and its durable services, an independent GetTaskExact
read is the evidence that a marker and audit version persisted. The Host reports
pending or unknown when it cannot prove the effect.

## Persistence and failure behavior

SQLite provides the atomic exact task command and durable outbox in this slice.
Backends without equivalent snapshot, compare-and-swap, grant, and audit
semantics fail closed for exact writes and private sanitized-message snapshots.
Legacy Host RPCs retain their existing behavior and do not inherit exact
authority or pagination semantics.

## Security and observability

Every exact operation intersects the installed manifest, current workspace
approval, and immutable human-reserved capability policy. Missing, revoked,
stale, undeclared, upgraded, wrong-workspace, or malformed authority denies
before domain data access or mutation. Read receipts and command audit IDs
contain bounded identities and digests; secret values and raw message payloads
are not included.

## Related decisions

- [Generic plugin Host boundary and capability approvals](../../../decisions/2026-08-31-generic-plugin-host-boundary.md)
