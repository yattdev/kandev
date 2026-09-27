---
id: "02-host-lease"
title: "Expose exact Host lease and GitHub credential adapter"
status: in_progress
wave: 2
depends_on:
  - 01-grant-ledger
plan: "plan.md"
requirements:
  - REQ-INTEGRATIONS-PROVIDER-SESSION-ACCESS-001
acceptance_criteria:
  - AC-INTEGRATIONS-PROVIDER-SESSION-ACCESS-001.3
  - AC-INTEGRATIONS-PROVIDER-SESSION-ACCESS-001.4
  - AC-INTEGRATIONS-PROVIDER-SESSION-ACCESS-001.5
  - AC-INTEGRATIONS-PROVIDER-SESSION-ACCESS-001.6
  - AC-INTEGRATIONS-PROVIDER-SESSION-ACCESS-001.9
system_design:
  - ../../specs/integrations/system-design/provider-session-access.md
---

# Task 02: Expose exact Host lease and GitHub credential adapter

## Summary

Add the optional versioned Host RPC/SDK contract and bind redemption to the
grant, active session, linked PR head and provider connection. Mint and revoke
a distinct one-repository GitHub App token per redeemed lease.

The Human selected Option A on 2026-09-27. This authorizes implementation
with fake provider fixtures; live token export still requires independent
review, QA, current-head CI, and plugin integration admission.

## In scope

- `provider-access/v1` exact wire and SDK methods with H6 capability check.
- Current linked PR/fork/head and run identity validation at issuance and
  redemption; no provider mutation.
- Uncached GitHub App token mint, transient plugin-only delivery, provider
  revoke on release/invalidation, and bounded pending-revocation receipt.
- Durable one-shot pre-mint intent; ambiguous provider response or Host crash
  leaves an unknown-mint receipt and cannot trigger an automatic second mint.
- GitLab PAT/unsupported-provider fail-closure and lifecycle revocation hooks.

## Out of scope

Plugin provider calls and task MCP provider-action tools.

## Acceptance

- The plugin can redeem one token only while exact grant, session, target,
  approval and connection identity remain current; no task agent sees it.
- HTTP fixtures prove one canonical repository and the minimum permissions,
  with no shared cache or PAT fallback.
- Provider revocation success and failure are distinguished; grant/session
  revocation fences new redemption immediately.
- Tests cover concurrent/replayed redemption, crash after pre-mint intent,
  ambiguous mint response, and conservative unknown-mint expiry without
  exposing a bearer or claiming confirmed revocation.

## Verification

```bash
(cd apps/backend && go test -race -count=1 ./internal/provideraccess ./internal/plugins ./internal/github ./pkg/pluginsdk)
(cd apps/backend && go test -count=1 ./internal/backendapp ./internal/mcp/server)
```

## Files likely touched

- `apps/backend/proto/kandev/plugin/v1/plugin.proto`
- `apps/backend/pkg/pluginsdk/`
- `apps/backend/internal/plugins/`
- `apps/backend/internal/provideraccess/`
- `apps/backend/internal/github/`
- `apps/backend/internal/backendapp/`

## Dependencies

Task 01 and a reviewed `provider-access/v1` fixture with the plugin owner.

## Risks

GitHub's token cannot be narrowed to a PR/run; Host crashes can leave an
issued token valid until GitHub expiry if revocation was not confirmed.

## Parallelism

`sequential`

## Inputs

- `apps/backend/internal/github/app_token_cache.go`
- `apps/backend/pkg/pluginsdk/host.go`
- `docs/decisions/2026-09-26-plugin-direct-provider-access.md`

## Results

The versioned optional Host RPC/SDK transport has exact typed request and
receipt fields, H6 capability checks, and an exact connected-plugin runtime
binding. The composed Host resolves the administrator grant, managed session,
current H6 approval, target task/repository, canonical PR/fork/head/run, and
current GitHub App connection at issuance and redemption. An uncached token is
minted for one verified repository with minimum permissions; token principal,
repository, and permissions are checked before a bearer can be returned.
GitLab PAT redemption fails closed. Production wires lifecycle fencing only;
the credential-bearing plugin RPC remains disconnected.

The Host persists one-shot mint intent and pre-export exposure/audit receipts,
serializes final exposure with grant and lease revocation, and revokes a token
that loses the final admission race. Grant replacement, explicit revocation,
workspace deletion, managed-session termination/deletion, plugin stop/error/
uninstall, and GitHub connection removal fence future access and attempt
exact-token revocation. A failed revocation or ambiguous mint retains a
redacted, expiry-bounded residual receipt. A restarted Host reports
`ErrRevocationUnconfirmed` for unexpired exported or ambiguous authority it
cannot revoke rather than claiming teardown succeeded. Audit correlation
stores only hashed request IDs and non-secret identities.

Terminal session callbacks now reserve the current state and original
execution or no-execution/no-start-attempt identity before invoking provider
revocation. Bootstrap also binds the error stamp. The repository blocks
executor rotation, prompt/recovery transitions and all four bulk cancellation
forms while the reservation is held; failed revocation releases it so the
same event can retry. Identity-less launch-failure callbacks fail closed when
provider revocation is configured. A committed claim includes a non-secret
target-state descriptor. Startup retries revocation against the Host exposure
ledger, then commits only the exact claim-owned terminal state and releases
the fence. An unconfirmed revocation leaves the claim fenced; an operator can
retry by restarting after provider expiry or restored provider revocation.

At source commit `a08961f0a04798effe598d254a3863e17b4c1ac2`, the
provider-access and PostgreSQL 16 store-conformance race suites, focused
task/service teardown race tests, docs validation, spec lint, and changed-package
Go lint passed. The later published `324ad996c164ac8e2083f9a09e734cb844735f44`
was the first independent Host Review target, not that test head. The
first broad orchestrator race run intermittently panicked in an unchanged
queued-message nil-Executor path; one structured full orchestrator race rerun
passed. Later independent review at `3562d5435cbc1bfb25af1c087ad41667737eb1fc`
found a stale pre-CAS revocation path. The ownership-reservation follow-up
is under verification. Fresh Review, distinct QA, exact-head CI/security, and the separately
owned plugin adapter are still required before live token export.
