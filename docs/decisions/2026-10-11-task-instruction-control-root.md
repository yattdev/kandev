# ADR-2026-10-11-task-instruction-control-root: Task-owned instruction control roots

**Status:** accepted
**Date:** 2026-10-11
**Area:** backend

## Context

Coordinator instructions are task-specific authority. A repository checkout can
contain Human-authored `AGENTS.md` files and local executors can share the Host
UID with an agent. Writing a task document into the checkout risks changing
tracked content, and a `0444` mode bit does not stop a same-UID process from
replacing it.

## Decision

The Host stores the authoritative task-instruction binding by task environment
and ownership generation, and publishes its mirror in a Host-owned control
directory outside every checkout. A managed worktree uses
`{tasksBase}/{taskDirName}/.kandev/task-instructions/AGENTS.md`; local and
remote executors require an environment-specific Host metadata root and cannot
derive that root from a shared checkout parent.

Before a governed prompt is admitted, the Host must prove the binding, mirror,
and executor guard match. A read-only mount, distinct OS principal, or
provider-attested equivalent is required. Same-principal chmod-only protection
and unsupported executor modes fail closed. Repository instructions remain
independent, Host-read sources that are injected before the task addendum.

## Consequences

Task instruction updates must carry the environment and ownership-generation
fence. Prompt routes re-resolve the current binding instead of retaining a
stale prompt snapshot. Coordinator content records parent and report-to IDs
only after Host lineage resolution. The projection is an aid to discoverability;
the database binding and final admission verification remain authoritative.

## Alternatives Considered

- Store the task document at the checkout root. Rejected because it can
  overwrite tracked Human instructions and varies by provider discovery rules.
- Rely on provider-native instruction discovery. Rejected because it cannot
  guarantee a second-turn reload or provide Host-wide authorization evidence.
- Treat `0444` as a write boundary. Rejected because an agent sharing the Host
  principal can change permissions, rename the file, or use a writable alias.
