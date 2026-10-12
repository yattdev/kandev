# ADR-2026-09-26-foreign-git-pointer-evidence-boundary: Separate foreign pointer evidence from Git recovery

**Status:** accepted
**Date:** 2026-09-26
**Area:** backend

## Context

A preserved checkout can point at another task's Git worktree administration
directory. Running Git in that checkout would read the foreign task's HEAD, index,
refs, and status. Existing archive manifests correctly reject nonreciprocal
registrations, but cannot safely be relaxed for this case.

## Decision

Introduce an internal-only evidence seam that admits an exact persisted worktree
and generation, walks its physical source tree without following links, and emits
a redacted receipt. It must not open the foreign administration target or invoke
Git. Foreign ownership, backlink, commit, index, queue, and PR evidence remain
unknown until a separately authorized comparison operation exists.

No HTTP, MCP, lifecycle, or live-task route is included in this decision. Repair
and retirement remain separate, authorized operations.

## Consequences

- Preserved source bytes gain a deterministic evidence record without changing
  either task's Git administration state.
- A receipt cannot establish cleanliness, index safety, or retirement eligibility.
- Cross-task access stays behind a subsequent security and authorization review.

## Alternatives considered

- **Reuse the archive source manifest after relaxing registration checks.** It
  invokes Git in the checkout and can attribute foreign state to the original task.
- **Open the pointed administration directory during initial inspection.** It
  discloses a foreign task before ownership authorization is established.
- **Repair the pointer before collecting evidence.** It can overwrite the only
  remaining clue to the original physical checkout.

## Related specifications

- [Requirements](../specs/workspaces/requirements/foreign-git-pointer-recovery.md)
- [System design](../specs/workspaces/system-design/foreign-git-pointer-recovery.md)
