---
status: draft
system: executors
requirements:
  - REQ-EXECUTORS-TASK-GIT-ISOLATION-001
  - REQ-EXECUTORS-TASK-GIT-ISOLATION-002
---

# Task Git Execution Isolation System Design

## Purpose and boundaries

This design makes the task Git projection an operating-system-enforced grant
for standalone Linux execution and validates conflicting Docker mounts. It uses
`internal/worktree.GitMetadataProjection` as the server-resolved source of
truth. The task system continues to own worktree allocation; agentctl continues
to own subprocess lifetime and terminal APIs.

## Requirement mapping

| Requirement | Design section |
| --- | --- |
| `REQ-EXECUTORS-TASK-GIT-ISOLATION-001` | [Isolation manifest and process launch](#isolation-manifest-and-process-launch) |
| `REQ-EXECUTORS-TASK-GIT-ISOLATION-002` | [Failure behavior](#failure-behavior) and [Docker mount validation](#docker-mount-validation) |

## Isolation manifest and process launch

`StandaloneExecutor.CreateInstance` validates every projection immediately
before creating the agentctl instance and converts it into a typed,
server-authored manifest. The manifest contains canonical workspace roots,
validated worktree `GitDir` paths, their task-private common directories, and
the original `SharedCommonDir` paths. It is carried in the internal agentctl
create-instance DTO and `config.InstanceConfig`; no agent request field can add
or widen an entry.

Validation opens and pins each grant's directory objects with stable file
descriptors, or an equivalent kernel-supported object reference, before helper
installation. The helper receives those pinned grants rather than authority to
resolve the original paths again. If a source object changes, cannot be pinned,
or no supported race-safe bind mechanism exists, instance creation fails closed.

`internal/agentctl/server/process/isolation` builds a Linux command wrapper
using the Kandev-distributed sandbox helper. The wrapper creates private mount
and PID namespaces, mounts a private proc view for that PID namespace, closes
or marks close-on-exec every inherited descriptor except an explicit,
read-only helper control channel, exposes runtime dependencies read-only, binds task workspaces and
validated task-private Git paths read-write, and binds every shared common Git
directory read-only before rebinding its validated private child. It neither
binds a parent source directory read-write nor accepts an uncanonical path.

`process.Manager.buildFinalCommand`, `ProcessRunner.Start`,
`ProcessRunner.StartPiped` (including LSP and `CombinedOutput` callers), and
interactive shell lifecycle paths use the same wrapper. Agentctl's workspace
tracker remains outside that wrapper because it is a trusted server component,
not an agent-controlled command boundary.

## Docker mount validation

`gitMetadataMounts` continues to provide the source common directory read-only
and the validated task Git administration directory read-write. A validator in
`container.go` runs after all runtime, profile, and plugin mounts are assembled.
For every read-write bind it canonicalizes both source and destination, rejects
any overlap with a shared common directory on either side (including ancestors,
descendants, and aliases), and permits only the exact validated task-private
administration source-and-destination rebind pair. This detects product
configuration conflicts independently of any local image or tag-plugin guard.

Clone-inside Docker has no host worktree namespace. `DockerExecutor` therefore
rejects non-empty host Git projections and lets the clone create private
`/workspace/.git` metadata.

## Failure behavior

Linux is the first supported standalone platform. Missing helper binaries,
unsupported user-namespace or mount setup, a malformed manifest, and an
unvalidated projection fail the instance before an ACP command, terminal, or
agent-owned process starts. Existing instances are not recreated automatically;
operators restart or recreate a task environment through normal lifecycle
actions after rollout.

## Security

The common Git root is a read-only operating-system mount, not merely a
filesystem-policy value. A direct shell write to sibling refs or reflogs fails
even when the agent knows the host path. Namespace isolation prevents writes
through host `/proc/<pid>/root` or related process-handle escape paths, and
descriptor closure prevents writes through inherited host descriptors.
Task-private object alternates reference the original object store read-only.
The projection resolver continues to reject symlink, pointer-swap, alternate,
and foreign-worktree attacks before the manifest is formed; pinned grants close
the validation-to-mount swap window.

## Observability

Launch failures emit a stable isolation reason: helper unavailable, unsupported
host capability, invalid manifest, or Docker mount conflict. The reason includes
the executor type but never repository, task, or filesystem paths in metrics
labels. Structured logs retain bounded path diagnostics for operators.

## Related decisions

- [ADR-2026-09-26-linux-task-git-execution-isolation](../../../decisions/2026-09-26-linux-task-git-execution-isolation.md)
- [Executor Container Security Options for User Namespace Support](../../../decisions/2026-08-18-executor-userns-security-options.md)
