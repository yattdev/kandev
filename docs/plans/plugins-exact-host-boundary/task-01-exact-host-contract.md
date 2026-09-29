---
id: "01-exact-host-contract"
title: "Exact Host contract and bounded task marker"
status: done
wave: 1
depends_on: []
plan: "plan.md"
requirements:
  - REQ-PLUGINS-EXACT-HOST-001
acceptance_criteria:
  - AC-PLUGINS-EXACT-HOST-001.1
  - AC-PLUGINS-EXACT-HOST-001.2
  - AC-PLUGINS-EXACT-HOST-001.3
  - AC-PLUGINS-EXACT-HOST-001.4
  - AC-PLUGINS-EXACT-HOST-001.5
  - AC-PLUGINS-EXACT-HOST-001.6
  - AC-PLUGINS-EXACT-HOST-001.7
system_design:
  - ../../specs/plugins/system-design/exact-host-boundary.md
---

# Task 01: Exact Host Contract and Bounded Task Marker

## Summary

Implement an additive exact plugin Host contract that binds reads to an
approved workspace and exposes complete, versioned task decision evidence.
Use one server-authorized, idempotent description-marker command with durable
audit and restart readback.

## In scope

- Exact protocol and SDK methods for capability context, inventory, decision
  evidence, session progress, sanitized messages, and task marker update.
- Host-derived installation identity and current capability approval.
- Bounded snapshots, opaque cursors, read receipts, resource versions, and
  fail-closed handling for incomplete or stale evidence.
- Server-owned grant validation, atomic task update, audit identity, and
  durable event recovery.

## Out of scope

- Coordinator-specific policy or data in Kandev core.
- Changes to legacy v1 Host authority or semantics.
- Additional task fields or writes, provider actions, production deployment,
  merge, or release.

## Acceptance

- Exact reads require current installation, manifest, and workspace approval
  and return bounded snapshot and version evidence.
- The exact command accepts only a description marker and leaves no effect on
  stale, revoked, mismatched, cross-workspace, or replayed authority.
- Restarted Host readback proves durable marker and audit state; unresolved
  event delivery remains pending or unknown.

## Verification

The completed verification is recorded in the sibling plan. Focused exact
normal and race tests, relevant full Go packages, proto generation, go vet,
changed-package lint, and independent two-workspace QA passed at the recorded
source head.

## Files likely touched

- apps/backend/proto/kandev/plugin/v1/plugin.proto
- apps/backend/pkg/pluginsdk/host.go
- apps/backend/internal/plugins/host_exact_*.go
- apps/backend/internal/task/repository/sqlite/exact_*.go
- apps/backend/internal/task/service/exact_task_command_outbox.go
- docs/public/plugins-manifest.md

## Dependencies

The existing plugin capability approval ledger and the generic Host boundary
decision provide the authority substrate and contract constraints.

## Risks

- Incomplete storage parity must deny exact writes instead of weakening the
  atomic precondition contract.
- Restart recovery must never report delivery without authoritative evidence.

## Parallelism

sequential

## Inputs

- [Exact Host requirements](../../specs/plugins/requirements/exact-host-boundary.md).
- [Exact Host system design](../../specs/plugins/system-design/exact-host-boundary.md).
- [Generic Host boundary decision](../../decisions/2026-08-31-generic-plugin-host-boundary.md).

## Results

Completed at Host source-contract head 8ee2296820054736df6e8ad98ca6bdc09688c49f.
The independent two-workspace QA and normal/race/package verification results
are recorded in the sibling plan.
