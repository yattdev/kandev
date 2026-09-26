---
status: draft
system: tasks
requirements:
  - REQ-TASKS-SAFE-FORCE-REMOVAL-001
  - REQ-TASKS-SAFE-FORCE-REMOVAL-002
  - REQ-TASKS-SAFE-FORCE-REMOVAL-003
  - REQ-TASKS-SAFE-FORCE-REMOVAL-004
---

# Safe Force Removal System Design

## Boundary

The task service owns authorization, preview, operation claims, quarantine
state, receipt persistence, and ordinary task visibility. Resource owners own
their inspections and stop/fence acknowledgements. The operation does not own
physical cleanup. It must not call `DeleteTask`, cascade-delete flows, or the
ordinary resource-cleanup executor.

This design consumes the shared authorization and redacted predicate-receipt
contracts only after PR #3937 lands at its merge commit. Until then, no code
may fork its paired `exact-retirement/preview` endpoint or claim integration.
Paired exact retirement remains a different operation with a replacement task
and physical-cleanup proof.

## Requirement mapping

| Requirement | Design sections |
| --- | --- |
| `REQ-TASKS-SAFE-FORCE-REMOVAL-001` | Preview and UI contract |
| `REQ-TASKS-SAFE-FORCE-REMOVAL-002` | Authorization and commit contract |
| `REQ-TASKS-SAFE-FORCE-REMOVAL-003` | Persistence and visibility |
| `REQ-TASKS-SAFE-FORCE-REMOVAL-004` | Claim, fencing, recovery, and replay |

## State and persistence

Add a task-local removal state with `active`, `retiring`, and `quarantined`.
`retiring` retains ordinary visibility but rejects every new admission. Only a
verified terminal fence may transition it to `quarantined`.

An independently keyed immutable receipt stores request hash, idempotency key,
actor kind and identity, target/workspace identity, target and admission
generations, preview digest, ordered redacted predicate receipts, retained
resource identities, timestamps, and terminal operation state. Receipt records
must not contain queue bodies, source bytes, credentials, or provider tokens.
The task row and its foreign keys remain in place; no force-removal migration
may add a cascade path.

## Preview and commit contract

Preview is read-only, single-target, no-store, and authorizes the target before
reading any inventory. It reports each fixed predicate as `PASS`, `BLOCKED`, or
`UNKNOWN`. Workspace inspection errors are `UNKNOWN`/`PRESERVED` evidence,
never a precondition to retry ordinary delete.

Commit accepts an exact UUID confirmation, a fresh preview token/digest, target
and admission generations, and an idempotency key. Human authority comes from
the authenticated request context. Agent authority additionally comes from
trusted transport-derived caller task/session identity and a single-use,
single-target, expiring grant. No body value establishes either identity.

## Claim, fence, and hide sequence

1. Authorize the exact visible target before inventory.
2. In one transaction, compare all expected generations, reserve the
   idempotency key, claim `retiring`, and place durable admission and cleanup
   holds.
3. Ask each runtime, queue, environment, worktree, cleanup, relationship, and
   PR-watch owner to prove its fence or terminal state. No step cleans a
   workspace or mutates preserved task-owned evidence.
4. If every required acknowledgement is terminal and exact, transactionally
   recheck generations, write the immutable receipt, transition to
   `quarantined`, and publish one logical-removal tombstone.
5. If any acknowledgement is blocked, unknown, or stale, record the result and
   retain `retiring` for recovery; the card remains visible. Retrying resumes
   the same operation only when its durable claim proves it is safe.

All writers that launch, resume, message, move, create an environment or
worktree, enqueue or dispatch work, run cleanup, or change PR watching must
consult the same durable gate. Read projections exclude `quarantined` tasks
after authorization; recovery reads use a separate admin-scoped path.

## Failure and recovery

An adapter failure, missing linked Git metadata, external-stop timeout,
unreadable owner, or contradictory identity is `UNKNOWN`. A live execution,
running cleaner, shared or foreign resource, or unresolved relationship is
`BLOCKED`. Neither result hides the card or grants physical cleanup.

A crash leaves the claim, holds, and receipts durable. Restart recovery may
read and resume the exact operation; it cannot infer quiescence or completion
from an absent process.

## Related decisions

- [ADR-2026-09-26-quarantined-task-force-removal](../../../decisions/2026-09-26-quarantined-task-force-removal.md)
- [Exact-task retirement boundary](guarded-exact-task-retirement.md) after its
  dependency lands.
