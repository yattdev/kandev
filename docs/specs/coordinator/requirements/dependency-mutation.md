---
status: draft
system: coordinator
created: 2026-10-25
owners:
  - kandev
---

# Coordinator dependency mutation Requirements

## Overview

A workspace Coordinator can maintain dependency edges between tasks it currently
watches so stale prerequisites can be cleared and new execution ordering can be
set without granting it general task mutation authority.

## Terminology

- **Dependent:** the task blocked by an edge.
- **Predecessor:** the task whose completion the dependent awaits.
- **Effective watch:** the intersection of the Coordinator's current workflow
  Watches and Projects scope.

## Requirements

### REQ-COORDINATOR-DEPENDENCY-001: Scoped edge maintenance

**Intent:** Let the current Coordinator maintain edges only within its managed
workspace scope.

#### Acceptance criteria

- **AC-COORDINATOR-DEPENDENCY-001.1:** When a Coordinator conversation has
  add or remove in its bound tool profile, it can mutate an edge only if both
  endpoints belong to its exact workspace and current effective watch.
- **AC-COORDINATOR-DEPENDENCY-001.2:** When either endpoint is absent, foreign,
  or outside the effective watch, the call is denied without revealing which
  condition failed and without changing an edge.
- **AC-COORDINATOR-DEPENDENCY-001.3:** A predecessor can be completed,
  archived, stopped, or sessionless; that state does not revoke permission to
  maintain its dependency metadata.
- **AC-COORDINATOR-DEPENDENCY-001.4:** Ordinary task agents remain limited to
  their own dependent task; authenticated external users retain their existing
  user-authorized dependency behavior.
- **AC-COORDINATOR-DEPENDENCY-001.5:** A dependency edge never expands the
  Coordinator's effective watch or any caller's authorization.
- **AC-COORDINATOR-DEPENDENCY-001.6:** An existing Coordinator conversation
  without the tools in its bound profile remains unable to call them.

### REQ-COORDINATOR-DEPENDENCY-002: Audited and consistent results

**Intent:** A mutation can be reconstructed and clients cannot regress to an
older dependency projection.

#### Acceptance criteria

- **AC-COORDINATOR-DEPENDENCY-002.1:** Every successful change, successful
  no-op, and denial has one durable audit result before the response is sent;
  audit failure prevents the edge write.
- **AC-COORDINATOR-DEPENDENCY-002.2:** A changed edge increments each affected
  endpoint's dependency revision once and returns authorized dependency
  projections for both endpoints. A no-op changes neither revision and emits no
  false dependency event.
- **AC-COORDINATOR-DEPENDENCY-002.3:** A cycle response reveals no task path;
  if that path contains an out-of-scope task, the response does not reveal the
  path's existence. Readback omits out-of-scope relation nodes.
- **AC-COORDINATOR-DEPENDENCY-002.4:** Concurrent mutation, watch revocation,
  task deletion, and session termination resolve in a serial order without an
  unauthorized edge, dangling edge, missing audit, or stale projection.
- **AC-COORDINATOR-DEPENDENCY-002.5:** Malformed tool requests are audited with
  only safe argument-shape evidence and do not disclose supplied identifiers
  through logs or errors.
- **AC-COORDINATOR-DEPENDENCY-002.6:** A client receiving dependency revisions
  out of order keeps the highest revision's projection.

## Out of scope

- General task editing, Host execution, credential access, and changes to the
  Coordinator's May do policy.
- A separate task-bound Coordinator designation or descendant-tree grant.
