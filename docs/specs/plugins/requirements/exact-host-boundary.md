---
status: active
system: plugins
created: 2026-09-29
owners:
  - kandev
---

# Exact Plugin Host Boundary Requirements

## Overview

This capability gives installed plugins a versioned, workspace-scoped Host
contract for reading task context and applying one narrowly bounded task
change. The plugin system owns installation identity, capability approval, and
the public Host contract. Task and workflow state remain owned by their
respective systems.

## Terminology

- **Exact operation:** A versioned Host operation with an explicit workspace,
  current capability authorization, bounded inputs, and authoritative version
  evidence.
- **Decision evidence:** A consistent snapshot of task relations and pending
  task transitions used to decide whether an exact task change is admissible.
- **Description marker:** The bounded, reversible description update allowed
  by this initial exact task-write contract.

## Requirements

### REQ-PLUGINS-EXACT-HOST-001: Workspace-bound reads and task marker updates

**Intent:** Let an approved plugin make a decision from complete Host evidence
and apply one exact, auditable task update without gaining authority over
unrelated workspaces or legacy Host operations.

#### Acceptance criteria

- **AC-PLUGINS-EXACT-HOST-001.1:** When an installed plugin requests capability
  context, the Host shall derive installation identity from the plugin
  connection and report the Host contract version, manifest digest, and
  workspace approval status and revision; receiving context alone shall grant
  no authority.
- **AC-PLUGINS-EXACT-HOST-001.2:** When an approved plugin reads exact
  workspaces, workflows, workflow steps, tasks, or sessions, each page shall be
  explicitly workspace-scoped, bounded, snapshot-bound, cursor-bound to its
  filters and approval context, and accompanied by resource versions and a
  Host read receipt. The Host shall deny expired or drifting snapshots,
  incomplete or unknown projections, and cross-workspace results.
- **AC-PLUGINS-EXACT-HOST-001.3:** When a plugin requests task decision
  evidence, the Host shall return complete relation endpoints and pending
  transition predicates with resource versions from one consistent snapshot;
  missing, unknown, or stale evidence shall not authorize a write.
- **AC-PLUGINS-EXACT-HOST-001.4:** Before an exact task update, the Host shall
  mint and enforce a server-owned, single-resolution grant bound to the
  installation, workspace, task, current capability approval, canonical
  action digest, idempotency identity, expiry, and applicable execution or
  assignment generation. Caller-supplied grants shall not be accepted as
  authority, and competing or mismatched replays shall have no effect.
- **AC-PLUGINS-EXACT-HOST-001.5:** When a plugin applies a description marker,
  the Host shall validate the exact task version, current approval, pending
  transition guard, provenance, grant, and idempotency key at commit, then
  return a typed outcome, authoritative resource version, and immutable audit
  identity. Rejected preconditions shall produce no task mutation or event.
- **AC-PLUGINS-EXACT-HOST-001.6:** When a plugin reads task progress or session
  messages through exact operations, the Host shall bind the result to the
  workspace, task, session, and applicable generation, and shall return only
  sanitized message content without credentials or unsanitized payloads. After
  restart, an effect shall be reported as durable only when an independent
  Host read confirms it; otherwise the outcome shall remain pending or
  unknown.
- **AC-PLUGINS-EXACT-HOST-001.7:** Adding exact Host operations shall not widen
  legacy v1 manifest authority or change v1 request semantics. An exact
  operation shall require its distinct declared host.v2 capability and
  current workspace approval.

## Out of scope

- Coordinator policy, scheduling, state, prompts, reports, and product UI.
- Global plugin authority, provider actions, broad task field updates, and
  writes other than the bounded description marker.
- Changes to legacy v1 Host behavior.
- Enabling exact writes on storage backends without equivalent atomic
  precondition and audit semantics.
