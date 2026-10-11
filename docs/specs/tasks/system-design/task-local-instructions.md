---
status: current
system: tasks
requirements:
  - REQ-TASKS-LOCAL-INSTRUCTIONS-001
created: 2026-10-11
updated: 2026-10-11
owners:
  - kandev
---

# Task-local Coordinator instruction system design

## Boundary and mapping

The task-environment binding is authoritative. The filesystem mirror and
provider prompt are derived projections that must be verified at final prompt
admission.

| Requirement | Design sections |
| --- | --- |
| `REQ-TASKS-LOCAL-INSTRUCTIONS-001` | Binding, control root, guard, prompt context, lineage |

## Binding and control root

The binding owns task, workspace, environment, environment ownership generation,
monotonic instruction version, exact UTF-8 content, and SHA-256 digest. Managed
worktrees place the mirror beneath the task root's `.kandev/task-instructions`
directory, which is a sibling of repository checkouts. Shared local and remote
workspaces require an explicit Host metadata root; code must not infer one by
taking the checkout parent.

Publication creates only real directories, rejects symlinked ancestors and
non-regular control files, writes a same-directory temporary file, syncs it,
renames it atomically, then reads it back against the binding. The Host never
writes a repository instruction file.

## Guard and admission

An executor supplies a proven read-only mount, distinct principal, or provider
attestation. `chmod` is defense in depth only. Before provider I/O, admission
rechecks the environment identity and generation, guard proof, mirror bytes,
digest, and repository source snapshot. Any failed check prevents dispatch;
queued and replacement paths rerun the same resolution.

## Prompt context and lineage

The Host discovers root-to-working-directory repository `AGENTS.md` sources
within the selected checkout, reads their exact bytes, then emits those sources
before a clearly labelled task addendum carrying the task/environment/version/
digest binding. Sibling repositories are never scanned as one source tree.

Coordinator template expansion receives parent and report-to IDs only from the
authoritative task relation store. If that store has no relation, issuance
stops pending a scoped Coordinator binding decision. A caller-supplied value is
checked as a fence and a changed parent is rejected.

## Related decisions

[Task-owned instruction control roots](../../../decisions/2026-10-11-task-instruction-control-root.md)
