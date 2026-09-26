# ADR-2026-09-26-quarantined-task-force-removal: Retain force-removed tasks in quarantine

**Status:** accepted
**Date:** 2026-09-26
**Area:** backend, protocol

## Context

Ordinary deletion must fail closed when worktree inspection is unavailable.
Overriding that check through the delete path would run cleanup and cascade
away queue, session, Git, PR/watch, and resource evidence. Operators still
need to remove one blocked card without treating unknown physical state as
clean.

## Decision

Force removal is a single-task logical quarantine. It retains the task row and
all task-owned evidence, records an immutable recovery receipt, and hides the
card only after an atomic admission claim and verified quiescence. It never
performs physical cleanup. Human and agent callers use one service commit path;
agents additionally require trusted caller identity and a target-specific,
expiring grant.

## Consequences

Task readers and every admission writer must honor durable quarantine state.
Operations interrupted after a claim remain visible and recoverable until
positive evidence permits completion. Physical task retirement remains a
separately authorized proof operation and may later consume the retained
receipt. This decision does not alter paired exact-task retirement.

## Alternatives Considered

- Add `force=true` to ordinary deletion: rejected because it can invoke cleanup
  and cascades when evidence is unavailable.
- Delete the task after copying selected evidence: rejected because full,
  lossless archival of every foreign key and resource relationship is not yet
  proven.
- Remove a worktree forcibly: rejected because it broadens physical deletion
  and cannot establish ownership from corrupt metadata.
