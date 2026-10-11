---
status: active
system: integrations
created: 2026-10-11
owners:
  - kandev
---

# Scoped GitHub PR URL Association

## Overview

Task parents can record an existing GitHub deliverable on their direct child
even when the attached source is a fork and the PR belongs to upstream.
Association records the deliverable; it does not grant branch-editing authority.
Integrations owns provider identity and persistence; task MCP owns trusted caller
admission. Existing manual and number-based association semantics remain valid.

## Requirements

### REQ-INTEGRATIONS-SCOPED-PR-URL-001: Trusted canonical association

**Intent:** Link one verified canonical PR without guessing fork identity or
overwriting another task's association.

#### Acceptance criteria

- **AC-INTEGRATIONS-SCOPED-PR-URL-001.1:** The existing management tool accepts
  `pr_url` and a full 40-character `expected_head_sha` together only for GitHub
  `link`. The existing required number must match the positive URL number.
  Number-based GitHub and GitLab link/unlink/replace remain compatible.
- **AC-INTEGRATIONS-SCOPED-PR-URL-001.2:** A trusted task-bound caller requires
  a persisted RUNNING session, a nonarchived parent and a nonarchived direct
  child in the same workspace. Unbound, self, unrelated, ancestor, grandchild,
  foreign, terminal, configuration and managed-conversation callers fail closed.
- **AC-INTEGRATIONS-SCOPED-PR-URL-001.3:** The child attachment identifies the
  source; the provider confirms target, source, canonical URL, number and head.
  Accept only strict HTTPS github.com URLs without credentials, ports, queries,
  fragments, encoded separators, extra segments or leading-zero numbers.
- **AC-INTEGRATIONS-SCOPED-PR-URL-001.4:** Fresh and expected heads must agree.
  An active stored row with a stale head or an occupied same-number slot with
  a different canonical URL is rejected, including conflicting tombstones.
- **AC-INTEGRATIONS-SCOPED-PR-URL-001.5:** Another nonarchived task's active
  canonical association blocks strict mode. Existing manual multi-task links
  remain supported. Do not detach, transfer, archive or repair another task.
- **AC-INTEGRATIONS-SCOPED-PR-URL-001.6:** Admission and collision checks fence
  insert/restore in one database transaction. Revocation during provider fetch
  is rejected. Concurrent strict claims have at most one winner.
- **AC-INTEGRATIONS-SCOPED-PR-URL-001.7:** A verified identical retry is
  idempotent; matching tombstones can restore. Open and merged PRs are eligible;
  closed-unmerged or identity-incomplete PRs are rejected. Contribution editing
  remains open-only. Existing outcome metadata and postcommit events survive.
- **AC-INTEGRATIONS-SCOPED-PR-URL-001.8:** Success reports persisted URL,
  attached repository, number, head and active links. Readback uncertainty fails
  with `state_known=false`. No provider write or implicit retry occurs.

## Scenarios

- GIVEN a live parent and fork-attached child, WHEN a verified upstream PR/head
  is linked, THEN the fork attachment ID and canonical upstream URL persist.
- GIVEN a merged PR with verified source/head, WHEN linked twice, THEN one
  association survives with merged attribution and no editing authority.
- GIVEN a stopped or unrelated caller, WHEN linking, THEN no provider read or
  association write occurs.
- GIVEN revocation after provider fetch, WHEN committing, THEN admission fails
  and rows/events/comparison targets remain unchanged.
- GIVEN stale heads or occupied canonical identity, WHEN linking, THEN existing
  links and automation state remain unchanged.
- GIVEN two child claims for one canonical PR, WHEN run concurrently, THEN at
  most one persists and neither overwrites the other's row.
- GIVEN postcommit readback failure, WHEN responding, THEN the caller receives
  uncertainty rather than fabricated success or automatic retry.

## Out of scope

Plugin Host mutations, enterprise GitHub hosts, GitLab URL mode, global PR
uniqueness, task launch/edit permissions, Docker executors and live deployment.

## Related contracts

- [System design](../system-design/github-scoped-pr-url-association.md).
- [PR association reads](github-pr-task-association-reads.md).
