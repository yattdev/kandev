# ADR-2026-10-11-scoped-pr-url-association: Core owns strict agent PR association

**Status:** accepted
**Date:** 2026-10-11
**Area:** backend, protocol, integrations

## Context

Fork attachments and canonical upstream PRs have different identities. A parent
agent needs to record a deliverable without guessing the URL or transferring
another task's link. Existing human linking intentionally supports multiple
tasks sharing a PR. Plugin evidence reads do not confer association authority.

## Decision

Extend the provider-neutral MCP management tool with an opt-in strict GitHub URL
mode. Core owns active direct-parent session admission, authenticated provider
resolution, source/head verification and transactionally fenced persistence.
Existing manual and number-based multi-task linking remains compatible.
Strict mode rejects competing active links and never silently transfers them.
Merged deliverable linking grants no contribution editing or launch authority.

Association insert/restore and ownership writers share database serialization;
scope and slot checks run in the same transaction as the strict mutation.
Keep the existing schema and outcome metadata. Emit effects after commit.
Ordinary association also rechecks its absent-slot precheck within the shared
write fence instead of overwriting a concurrent link. Explicit manual upserts
retain their own semantics. Strict successful readback must match committed
identity/head evidence and the active link snapshot.

## Consequences

One public management surface avoids an obsolete parallel API. Database locking
adds short serialization to association writes, while provider reads stay outside
the write transaction. Future plugin association operations must reuse this core
boundary with explicit Host capability admission; they cannot use generic task
writes or direct database access. Requirements live in the integration spec.

## Alternatives Considered

- Restore the old URL-link tool: duplicates current API and misses race fencing.
- Add a plugin plus Host mutation immediately: expands SDK/grants/release scope
  without removing the need for core checks.
- Global PR uniqueness: breaks legitimate existing manual associations.
