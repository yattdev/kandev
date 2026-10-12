---
status: active
system: workspaces
created: 2026-09-26
owners:
  - kandev
---

# Foreign Git pointer recovery evidence

## Overview

A preserved checkout can contain a `.git` pointer to another task's linked-worktree
administration directory. Its Git status, index, HEAD, and branch then describe the
foreign owner. Recovery begins with bounded evidence collection; it does not infer
that the original checkout is clean or safe to retire.

## Requirements

### REQ-WORKSPACES-FOREIGN-GIT-RECOVERY-001: Exact evidence admission

An evidence request identifies one persisted task, environment, repository, and
worktree record plus its expected generation. Callers cannot select a filesystem
path. A missing, cross-task, or stale inventory fails without disclosing another
task's checkout or administration path.

#### Acceptance criteria

- **AC-WORKSPACES-FOREIGN-GIT-RECOVERY-001.1:** The evidence seam reloads the
  worktree record by ID and rejects a task mismatch or stale generation before a
  filesystem read.
- **AC-WORKSPACES-FOREIGN-GIT-RECOVERY-001.2:** The initial implementation has
  no HTTP, MCP, or lifecycle registration. Enabling cross-task inspection requires
  separately reviewed authorization and a fresh exact inventory read.

### REQ-WORKSPACES-FOREIGN-GIT-RECOVERY-002: Bounded physical evidence

The evidence seam records physical source identity without consuming the foreign
Git administration state.

#### Acceptance criteria

- **AC-WORKSPACES-FOREIGN-GIT-RECOVERY-002.1:** A no-follow walk records files,
  directories, symlinks, modes, sizes, and content or link SHA-256 values,
  including ignored content. It excludes only the top-level `.git` pointer from
  source-tree hashing and records pointer metadata separately.
- **AC-WORKSPACES-FOREIGN-GIT-RECOVERY-002.2:** A nested Git boundary, special
  file, unreadable path, changed checkout path, cancellation, or resource limit
  leaves the receipt incomplete and `UNKNOWN`. It never yields a clean result.
- **AC-WORKSPACES-FOREIGN-GIT-RECOVERY-002.3:** The seam never invokes Git in the
  original checkout, follows or opens the foreign administration target, reads an
  index/HEAD/ref/status, or writes to either physical root.
- **AC-WORKSPACES-FOREIGN-GIT-RECOVERY-002.4:** The receipt contains no source
  bytes, command output, credentials, or queue bodies. Foreign owner and backlink
  evidence remain `UNKNOWN` until a separately authorized comparison provides it.

## Scope and exclusions

This is evidence collection only. It does not activate a route, inspect a live
preserved task, repair a `.git` pointer, create a replacement worktree, archive or
retire a task, or classify content as tracked or untracked. Candidate commits,
provider PRs, queues, and index safety are comparison-stage concerns.
