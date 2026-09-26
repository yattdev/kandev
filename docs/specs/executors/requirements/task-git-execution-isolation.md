---
status: draft
system: executors
created: 2026-09-26
owners:
  - kandev
---

# Task Git Execution Isolation Requirements

## Overview

Task worktrees share a source repository's Git object and administration tree.
The executor system owns the process and filesystem boundary that lets an agent
commit to its task checkout without changing sibling worktrees or the source
repository. This contract belongs to executors because it applies to standalone
process launch and Docker mount construction, not to task ownership itself.

## Terminology

- **Shared common directory:** The source repository Git common directory used
  by one or more linked task worktrees.
- **Task-private metadata:** The task-owned Git administration and private
  common directory used for its index, refs, reflogs, and new objects.
- **Agent-controlled process:** The ACP agent, an agent-requested process, or
  an interactive terminal or shell started for that agent instance.
- **Isolation manifest:** Server-resolved filesystem grants for one agent
  instance. Agents cannot provide or expand its paths.

## Requirements

### REQ-EXECUTORS-TASK-GIT-ISOLATION-001: Strict task Git write boundary

**Intent:** Agents need normal Git operations without authority to alter shared
or sibling Git metadata.

**User story:** As an operator, I want a task agent's Git changes confined to
its task checkout so that concurrent tasks cannot corrupt each other.

#### Acceptance criteria

- **AC-EXECUTORS-TASK-GIT-ISOLATION-001.1:** When a standalone worktree agent
  starts on a supported Linux host, the system shall run every agent-controlled
  process with a server-resolved isolation manifest that makes its shared common
  Git directories read-only and its validated task-private metadata writable.
- **AC-EXECUTORS-TASK-GIT-ISOLATION-001.2:** When an agent-controlled process
  attempts a direct write to a sibling ref, sibling reflog, or another path in
  a shared common Git directory, the system shall deny the write.
- **AC-EXECUTORS-TASK-GIT-ISOLATION-001.3:** When an agent uses a validated
  task checkout, the system shall permit native `git add`, `git commit`, and
  `git fetch`, including index, ref, and reflog lock behavior; the host checkout
  shall observe a successful task commit.
- **AC-EXECUTORS-TASK-GIT-ISOLATION-001.4:** When a task has attached
  repositories, the system shall apply the same isolation independently to each
  validated checkout and shall deny writes across their shared or sibling Git
  metadata.

### REQ-EXECUTORS-TASK-GIT-ISOLATION-002: Fail-closed executor enforcement

**Intent:** A missing isolation mechanism must not silently launch an agent
with broader filesystem authority.

#### Acceptance criteria

- **AC-EXECUTORS-TASK-GIT-ISOLATION-002.1:** When a standalone worktree
  instance cannot establish its supported isolation environment, the system
  shall refuse to start that instance and shall report an actionable executor
  failure without falling back to an unrestricted host process.
- **AC-EXECUTORS-TASK-GIT-ISOLATION-002.2:** When a Docker host-worktree
  configuration contains a read-write mount covering a shared common Git
  directory, the system shall reject the configuration unless the mount is a
  validated task-private child grant.
- **AC-EXECUTORS-TASK-GIT-ISOLATION-002.3:** When Docker clones a repository
  inside its container workspace, the system shall reject host worktree
  metadata projections and shall use only container-private Git metadata.
- **AC-EXECUTORS-TASK-GIT-ISOLATION-002.4:** When a task-private metadata
  pointer, alternate object path, or linked-worktree registration is swapped,
  symlinked, malformed, or foreign, the system shall refuse the instance before
  granting filesystem access.

## Out of scope

- Windows and macOS standalone sandbox support.
- Recreating or mutating a live task session while this capability is rolled
  out.
- Deployment, image-tag-plugin configuration, or host-wide security policy
  changes outside the product's documented compatibility checks.
