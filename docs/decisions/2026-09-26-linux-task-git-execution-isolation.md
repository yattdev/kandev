# ADR-2026-09-26-linux-task-git-execution-isolation: Enforce task Git grants in Linux mount namespaces

**Status:** accepted
**Date:** 2026-09-26
**Area:** backend

## Context

Linked task worktrees share source Git metadata. A private Git metadata clone
lets normal Git commands avoid source ref writes, but standalone agentctl starts
agent commands and terminals as unrestricted host processes. An agent can bypass
that clone with a direct write to the shared common directory. Docker mount
configuration can also be widened by a runtime or plugin mount unless the final
mount set is validated.

## Decision

Kandev will distribute a version-pinned Linux per-instance sandbox helper from
the reviewed Kandev release artifact, with its digest/provenance recorded in the
package manifest. Before launch, agentctl verifies the expected version and
digest, executable ownership and mode, helper self-reported capability, and the
host's required user-namespace, mount, PID-namespace, and proc support. Kandev
will fail closed for
standalone worktree instances that require task Git isolation when it cannot
create the required mount namespace. Agentctl will apply the same server-authored
manifest to the ACP process, agent-owned process runner including piped/LSP and
combined-output calls, and interactive shells. The helper creates private mount
and PID namespaces, a private proc view, and closes inherited writable
descriptors before executing agent-controlled code.
Shared Git common directories are read-only; only validated task workspace and
private Git administration paths are writable. Docker validates the final mount
set by canonical source and destination overlap and rejects read-write coverage
of a shared common directory except the exact validated private administration
source-and-destination rebind pair. Filesystem objects are pinned, or
equivalently installed race-safely, between validation and helper mount.

The first supported scope is Linux. Existing live standalone sessions are not
recreated automatically. Helper packaging and compatibility checks are product
rollout work, not deployment authorization.

Proposed operator activation is: verify the release artifact and host capability
check on a non-live task environment; enable protected standalone launches for
new instances; then restart or recreate existing task environments through the
normal operator lifecycle action. Proposed rollback is: stop admitting new
protected standalone launches, retain existing sessions unchanged, and return
new work to the prior supported executor only after its ordinary safety checks.
Neither sequence authorizes automatic session mutation or deployment.

## Consequences

- Direct sibling ref and reflog writes are denied by the OS boundary while
  ordinary task Git operations continue.
- A host without the helper or required namespace support cannot launch a new
  protected standalone worktree instance.
- Process, terminal, and shell launch paths share one isolation contract.
- Distribution packaging and Linux capability diagnostics become required
  executor responsibilities.
- Package provenance, helper capability checks, and operator activation and
  rollback instructions must be reviewed before policy adoption.

## Alternatives Considered

- **Metadata-only projection:** Does not constrain direct host-path writes.
- **Run standalone agents under a shared unprivileged account:** Changes file
  ownership and still exposes every path readable by that account.
- **Use Docker for all standalone tasks:** Changes executor selection, lifecycle,
  and operator compatibility beyond the local execution contract.
- **Rely on a local tag-plugin image guard:** Does not enforce the upstream
  product boundary or cover arbitrary host installations.
