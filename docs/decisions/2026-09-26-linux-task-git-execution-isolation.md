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

Kandev will provide a Linux per-instance sandbox helper and fail closed for
standalone worktree instances that require task Git isolation when it cannot
create the required mount namespace. Agentctl will apply the same server-authored
manifest to the ACP process, agent-owned process runner, and interactive shells.
Shared Git common directories are read-only; only validated task workspace and
private Git administration paths are writable. Docker validates the final mount
set and rejects read-write coverage of a shared common directory except a
validated private child grant.

The first supported scope is Linux. Existing live standalone sessions are not
recreated automatically. Helper packaging and compatibility checks are product
rollout work, not deployment authorization.

## Consequences

- Direct sibling ref and reflog writes are denied by the OS boundary while
  ordinary task Git operations continue.
- A host without the helper or required namespace support cannot launch a new
  protected standalone worktree instance.
- Process, terminal, and shell launch paths share one isolation contract.
- Distribution packaging and Linux capability diagnostics become required
  executor responsibilities.

## Alternatives Considered

- **Metadata-only projection:** Does not constrain direct host-path writes.
- **Run standalone agents under a shared unprivileged account:** Changes file
  ownership and still exposes every path readable by that account.
- **Use Docker for all standalone tasks:** Changes executor selection, lifecycle,
  and operator compatibility beyond the local execution contract.
- **Rely on a local tag-plugin image guard:** Does not enforce the upstream
  product boundary or cover arbitrary host installations.
