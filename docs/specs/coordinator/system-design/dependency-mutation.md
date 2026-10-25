---
status: draft
system: coordinator
requirements:
  - REQ-COORDINATOR-DEPENDENCY-001
  - REQ-COORDINATOR-DEPENDENCY-002
---

# Coordinator dependency mutation System Design

## Purpose and boundaries

The Coordinator system owns the principal, bound tool profile, and current
effective watch. The task system owns dependency edges, revisions, mutation
serialization, and task events. The MCP server and handler transport the
attested principal; arguments never supply caller identity.

## Requirement mapping

| Requirement | Design section |
| --- | --- |
| `REQ-COORDINATOR-DEPENDENCY-001` | [Authority](#authority) |
| `REQ-COORDINATOR-DEPENDENCY-002` | [Mutation and audit](#mutation-and-audit), [Projection](#projection) |

## Authority

`internal/mcp/profile` accepts the two dependency tool names in a Coordinator
binding. `internal/mcp/server` registers them only when named by that binding.
`internal/mcp/handlers` checks the server-attested Coordinator principal and
the binding again on dispatch. A current effective watch requires both
workflow Watches and Projects membership; each endpoint is re-read in the
principal's exact workspace at the mutation boundary. A previously bound
conversation is never given new tools in place.

## Mutation and audit

The task-domain dependency writer owns one transaction boundary for edge
validation, current scope and liveness checks, cycle detection, edge write,
both endpoint revision updates, authorized snapshots, and the audit result.
Scope tightening and task deletion serialize with that boundary. A no-op uses
the same validation and writes an audit result but leaves revisions and events
unchanged. A denial before opening the graph transaction writes a short audit
row with no unverified target identifiers. An audit insertion error rolls back
the mutation. MCP schema-invalid calls reach an audit-only backend action;
they are never forwarded to a mutating action for auditing.

## Projection

The response contains both endpoint projections captured by the committed
transaction. The task event and boot/list/read surfaces carry each endpoint's
dependency revision. Consumers accept a projection only when its revision is
newer than the stored one; an equal revision is idempotent and an omitted
projection leaves the current one intact. Coordinator readback filters every
relation node independently against its effective watch. Cycle errors omit the
path, and a path containing hidden nodes uses the same non-disclosing denial
as a hidden endpoint.

## Related decisions

- [Coordinator dependency maintenance](../../../decisions/2026-10-25-coordinator-dependency-edges.md)
