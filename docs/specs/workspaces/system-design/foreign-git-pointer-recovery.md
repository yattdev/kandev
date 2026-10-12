---
status: current
system: workspaces
requirements:
  - REQ-WORKSPACES-FOREIGN-GIT-RECOVERY-001
  - REQ-WORKSPACES-FOREIGN-GIT-RECOVERY-002
---

# Foreign Git pointer recovery evidence design

## Admission boundary

`worktree.Manager.InspectForeignGitWorktree` is an internal, unregistered seam.
It accepts a request ID, task ID, persisted worktree ID, and expected worktree
generation. It reloads the record before opening its recorded path and rejects
identity or generation disagreement. The request deliberately has no path field.

This seam does not establish authorization to disclose a second task. A future
handler must first authorize the exact owner and workspace, resolve both original
and foreign inventories, and revalidate their generations. It is a separate change.

## Evidence capture

The manager opens the recorded checkout using the existing no-follow directory
handles. It reads the top-level `.git` file only to record a hashed pointer and
its lexical administration path. It never opens that administration path, so its
backlink, task owner, Git state, and registration status are explicitly unknown.

The physical walker sorts entries and records a redacted manifest of regular
files, directories, and symlinks. Files receive SHA-256 content hashes; links
receive hashes of their target text. The top-level `.git` entry is not part of the
source tree. A second `.git` entry is a nested-boundary reason and blocks a
complete result. The walk is limited to 100,000 entries, 1 GiB per regular file,
and 4 GiB total regular-file content. Unsupported entries, read failures,
cancellation, a checkout-path change, and any resource limit produce an
incomplete receipt. `Complete` means the physical manifest and pointer were
captured consistently; it can be true while `Overall` is `BLOCKED` because
foreign administration access remains unauthorized.

The receipt has only `PASS`, `BLOCKED`, and `UNKNOWN` values. This slice cannot
produce `PASS`: a successfully parsed pointer is `BLOCKED` because foreign admin
access is unauthorized; malformed or incomplete observations are `UNKNOWN`.

## Non-goals and follow-up

The seam has no Git subprocess, route registration, persistence, mutation, or
live-target invocation. A later comparison operation owns candidate commits,
provider verification, queue inventory, foreign-owner/backlink confirmation, and
independent recheck before any repair or retirement.

## Requirement mapping

| Criteria | Design evidence |
| --- | --- |
| 001.1, 001.2 | Persisted-ID reload, generation check, no route wiring |
| 002.1 | No-follow deterministic physical manifest |
| 002.2 | Incomplete reason codes and path revalidation |
| 002.3, 002.4 | No foreign-admin open and redacted receipt fields |
