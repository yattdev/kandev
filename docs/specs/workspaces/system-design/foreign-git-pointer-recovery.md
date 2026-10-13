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

Ownership admission retains the authenticated task-root and marker descriptors,
then descends to the checkout through relative no-follow directory handles. The
chain stays open through capture, marker revalidation, and the persisted-generation
reload. A final bounded walk compares every observed path, native object identity,
mode, size, change metadata, and content or link digest, including the `.git`
pointer. File metadata comes from the opened descriptor; link targets are bound
to no-follow link identity. Unsupported native identity fails closed. Snapshots
stay in memory and are omitted from the redacted receipt. Each walk retains the
same entry and byte limits. These observations detect replacements visible at
the checks; they do not prove filesystem quiescence, every transient replacement,
or index, queue, or provider provenance.

## Non-goals and follow-up

The seam has no Git subprocess, route registration, persistence, mutation, or
live-target invocation. A later comparison operation owns candidate commits,
provider verification, queue inventory, foreign-owner/backlink confirmation, and
independent recheck before any repair or retirement.

Existing comparison metadata cannot supply that historical admission:
`ComparisonTarget` identifies current repository/branch bindings, `PRBase.OID`
is a transient base consistency check, and `RemoteContribution` validates an
open provider change. The comparison-only head ref captures an observed commit,
without accepted-state, tree, original-task, or generation attestation. Archive
source manifests hash changed paths and require reciprocal registration; they
are neither full accepted trees nor an immutable provider ledger. Current rows,
caller completeness/authentication claims, and matching hashes therefore leave
historical comparison unavailable. An owner-produced immutable accepted-head/tree
receipt bound to the original task generation needs its own reviewed contract
before composition; the existing unavailable comparison and exact-retirement
UNKNOWN projections remain in force meanwhile.

## Requirement mapping

| Criteria | Design evidence |
| --- | --- |
| 001.1, 001.2 | Persisted-ID reload, generation check, no route wiring |
| 002.1 | No-follow deterministic physical manifest |
| 002.2 | Incomplete reason codes and path revalidation |
| 002.3, 002.4 | No foreign-admin open and redacted receipt fields |
