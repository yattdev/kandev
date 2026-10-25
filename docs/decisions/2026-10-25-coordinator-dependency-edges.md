# ADR-2026-10-25-coordinator-dependency-edges: Coordinator dependency maintenance

**Status:** accepted
**Date:** 2026-10-25
**Area:** backend, protocol, security

## Context

A workspace Coordinator needs to clear or add dependency edges between tasks it
currently manages, including edges to completed predecessors. The existing
Coordinator conversation has a server-attested principal, an immutable bound
tool profile, and effective Watches and Projects. Ordinary task agents may
mutate only their own dependent task. The earlier dependency plan proposed a
separate task-bound designation and descendant-tree scope, while the current
Coordinator is a conversation principal with a different scope model.

The phase-2 control policy otherwise confines Coordinator writes to proposals
for a manager. Dependency maintenance is a narrowly authorized exception to
that policy, approved for this capability rather than a general permission to
mutate tasks.

## Decision

The current server-attested Coordinator conversation principal may call only
policy-bound add and remove dependency tools. At the write boundary, both edge
endpoints must be in the principal's exact non-empty workspace and in its
current effective Watches and Projects. Edge membership itself never grants
authority. The tool profile is bound when the conversation opens; an older
binding is not widened in place.

Every attempt has a durable, non-leaking audit result, and a changed or no-op
edge result is recorded with the write transaction. A changed result publishes
ordered dependency projections for both endpoints. Denials and storage failures
fail closed. Ordinary task agents remain self-only, while authenticated
external users retain their existing authorization path. No Host execution,
credential, or workspace-wide mutation authority follows from this decision.

## Consequences

The Coordinator catalog and execution guard must both enforce the bound tool
list. The task dependency writer must serialize authorization, cycle checks,
edge mutation, endpoint revisions, and audit. Effective scope changes must be
ordered with a pending mutation. Readback and cycle errors must not disclose
out-of-scope relation nodes. Public documentation must describe the limited
Coordinator capability once it ships.

This is a scoped exception to the phase-2 write rule in
[ADR-2026-09-29-coordinator-phase-2-control](2026-09-29-coordinator-phase-2-control.md);
that rule continues to govern its other actions.

## Alternatives Considered

- A separate task-bound `workspace_coordinator_grants` designation and
  descendant-tree scope would introduce a second Coordinator identity and
  authority model. It was the earlier plan but was not selected for this
  capability.
- Allowing the current Coordinator to mutate any task in its workspace would
  exceed its effective Watches and Projects and disclose or change tasks it
  does not manage.
- Reusing ordinary self-only task-agent authority would not permit maintenance
  of a peer dependent task.
