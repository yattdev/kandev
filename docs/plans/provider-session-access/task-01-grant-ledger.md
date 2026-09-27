---
id: "01-grant-ledger"
title: "Persist and authorize provider grants"
status: in_progress
wave: 1
depends_on: []
plan: "plan.md"
requirements:
  - REQ-INTEGRATIONS-PROVIDER-SESSION-ACCESS-001
acceptance_criteria:
  - AC-INTEGRATIONS-PROVIDER-SESSION-ACCESS-001.1
  - AC-INTEGRATIONS-PROVIDER-SESSION-ACCESS-001.2
  - AC-INTEGRATIONS-PROVIDER-SESSION-ACCESS-001.7
system_design:
  - ../../specs/integrations/system-design/provider-session-access.md
---

# Task 01: Persist and authorize provider grants

## Summary

Add a durable, administrator-managed grant whose generation, expiry and
revocation are enforced for an exact plugin installation and managed session.
Record redacted grant and lease-admission audit without minting a credential.

## In scope

- Replayable SQLite/PostgreSQL schema, grant/lease/audit models and store.
- Authenticated grant create/list/revoke with workspace authorization.
- Exact installed-plugin, H6 approval, managed-conversation session, target
  task/repository/link and provider-connection admission.

## Out of scope

Provider token redemption, provider API mutation, and plugin adapter.

## Acceptance

- A cross-workspace, wrong installation/session, terminal session, stale
  approval or grant generation request is denied without a lease.
- Replacing a grant atomically revokes its predecessor and preserves a
  monotonic generation; same-key replay does not create a second lease.
- Durable rows and audit contain no token, header, or raw idempotency key.

## Verification

```bash
(cd apps/backend && go test -race -count=1 ./internal/provideraccess ./internal/plugins ./internal/db)
(cd apps/backend && go test -count=1 ./internal/backendapp)
```

## Files likely touched

- `apps/backend/internal/provideraccess/`
- `apps/backend/internal/db/schema.go`
- `apps/backend/internal/plugins/approval_service.go`
- `apps/backend/internal/backendapp/`

## Dependencies

None.

## Risks

The managed conversation's task metadata and session lookup must be resolved
through service/repository APIs, never trusted from plugin request fields.

## Parallelism

`sequential`

## Inputs

- `docs/specs/integrations/system-design/provider-session-access.md`
- `apps/backend/internal/task/service/agent_conversations.go`
- `apps/backend/internal/plugins/approval_service.go`

## Results

Grant generation replacement, exact-workspace revocation, non-secret
lease/audit storage and provider exposure receipts are implemented locally
with focused race-enabled tests. A negative test preserves the residual
exposure after failed provider revocation, lease expiry and store reopen.
Storage now caps leases at five minutes and serializes final exposure
admission with grant/lease revocation. A losing admission path invokes an
exact-token revocation callback before a caller could export a bearer; its
failure is redacted. Final admission also transactionally compares the full
Host-verified grant/lease identity, including session, target, approval and
connection generations. The active-lease read is inspection only. SQLite race
tests and an isolated PostgreSQL 16 row-lock regression cover these cases.
The administrator grant create/list/revoke API requires a real identity and
`secret.manage` workspace scope. Creation derives the installed plugin identity
and rechecks its current H6 approval, target task repository attachment, and
active GitHub App connection; expiry is capped at 24 hours. The API is live for
non-secret grant records only. Grant creation, replacement, and revocation
record audit in the mutation transaction. Exact PR/fork/head/run admission and
runtime-aware revocation are implemented in Task 02. The task-owned PostgreSQL
16 store-conformance suite and provider-access race suite passed at source
commit `a08961f0a04798effe598d254a3863e17b4c1ac2`. The later published
`324ad996c164ac8e2083f9a09e734cb844735f44` was the first independent
Host Review target; it must not be confused with that test head. Review
fixup, distinct QA, and exact-head CI remain pending. Production credential
redemption remains disconnected.
