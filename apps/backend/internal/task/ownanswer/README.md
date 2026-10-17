# Isolated approval-link support repair

This custom fork module preserves the existing closed native-answer reconciler.
Agent comments do not prove historical linkage to a question. The approval-link
path records a **new authorization**, explicitly declaring that historical
linkage is unavailable. It is restricted to the single reviewed case returned by
`ApprovalLinkCase`; it is not a general comment interpreter or privileged override.

The private HTTP creation route is
`POST /api/v1/tasks/:id/approval-link-grants`. It requires the existing server
authentication middleware, a real browser-session administrator, and normal
workspace `task.write` access. The creator must remain the administrator who
owns that workspace, in the same organization. Synthetic auth-disabled identities, PATs,
instance operators, task-session principals, unknown fields, duplicate keys, and
foreign tuples are refused. An administrator must explicitly supply the existing
option ID and the `new-authorization/historical-linkage-unavailable/v1` purpose.
No option is inferred from comment text. Session incarnation, creator identity,
timestamps, evidence snapshots, and the original receipt digest are server-owned.
The transaction reads current non-secret browser-session/account authority,
rejects revoked or expired sessions and account changes, and records an authority
revision. It reads no credential token or password material. Consumption rechecks
that authority and workspace ownership, including at its commit boundary.

Grants expire after 15 minutes. There is one durable grant per question message;
expiry or revocation does not make the question eligible for another grant.
The durable state is `issued`, `consumed`, or `revoked`. This implementation does
not expose an API to reset or reissue a grant.
`DELETE /api/v1/tasks/:id/approval-link-grants/:grant_id` lets the same
authenticated creator/browser session revoke an unused grant under the same
workspace authorization. Revocation and consumption use the same transaction
locks. Revocation is idempotent and cannot undo consumption.

Consumption uses the existing own-answer MCP path with both
`approval_link_grant_id` and `approval_link_operation_id`. The ordinary own-task
and session authorization, disconnected-question checks, and no-live-waiter guard
remain required. The backend-owned MCP execution must match a current active
executor record. The transaction rechecks the incarnation, entire stored bodies,
context, options, metadata, and original receipt. It atomically consumes the
grant and commits the answer-link CAS. A conflicting operation fails; the same
operation reads the same persisted result after restart without spending again.
Session and executor row locks remain held through commit. A final fence checks
their identities and update revisions after the grant/link writes; replacement,
re-registration, terminal state, or changed creator authority rolls back the
entire transaction, including the grant consumption.

Original message bodies and both comment pairs remain unchanged. Only normal
answer-link metadata is added. No queue entry is dispatched, consumed, reordered,
or replayed; no task or session is resumed. A grant is evidence for linkage only.
It does not authorize installation, deployment, inference, or execution of the
operation described by the selected answer.

The public entry points enforce the fixed case. Repository transaction workers
are also exercised with disposable synthetic SQLite fixtures, separately from
the HTTP authentication and exact-case ingress checks. Positive transaction
tests do not represent a repair of any live record. PostgreSQL verification
requires a separately approved isolated test database; it is not implied by the
SQLite results.

Validation from `apps/backend`:

```sh
KANDEV_TEST_POSTGRES_DSN='' go test -race ./internal/task/repository/sqlite ./internal/task/handlers ./internal/mcp/handlers ./internal/mcp/server -run 'TestApprovalLink|TestOwnAnswer' -count=1
go run ./cmd/sqlguard ./internal
KANDEV_TEST_POSTGRES_DSN='' go test -race ./internal/persistence/storeconformance -count=1
```

This development change has no live activation approval. Independent review,
functional QA, and a separate concrete activation review remain required before
any installation or live grant/settlement. No upstream defect or maintainer
approval is asserted.
