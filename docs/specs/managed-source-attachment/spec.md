---
status: approved
created: 2026-10-11
updated: 2026-10-11
owner: Kandev
---

# Managed source attachment repair

A registered provider repository can retain a saved managed `LocalPath` after
its clone disappears. Source attachment currently skips that path and branch
attachment either fails worktree creation or defers an empty path until launch.
Both attachment operations must materialize the source immediately through the
existing authenticated task/session cloner when their environment is ready.

This is core backend behavior. A server hosted in Docker uses its container
filesystem for local/worktree agents; that deployment is distinct from a task
using the Docker executor. Remote-executor materialization is unchanged.

## Behavioral contract

1. Source attachment on local/worktree tasks repairs an absent exact owned
   managed provider clone, including a saved nonempty path, and exposes readable
   content in the task workspace.
2. Branch attachment on worktree tasks repairs absent or empty managed paths
   and returns a registered worktree using the selected branch/base.
3. Reuse a valid managed checkout. Validate exact repository/workspace/provider
   path identity, filesystem ancestry, Git directory ownership and origin before
   reuse or clone. Reject foreign, occupied, symlinked and redirected paths.
   Filesystem errors do not authorize repair or deletion.
4. Preserve explicitly user-managed local repositories even when they carry
   provider metadata. A missing local source fails without redirect or clone.
5. Clone credentials retain exact task, session, repository and workspace scope.
   Missing required clone scope/cloner, denied credentials, cancellation, invalid clone output
   or failed path persistence are failures, never successful deferral.
6. Validate successful clone output before persisting its canonical local path.
   A failed attachment compensates newly added durable attachments and task-owned
   worktrees; existing sources and attachments remain intact. A shared managed
   clone may remain after a later attachment failure. Exact source retries reuse
   the same materialization.
7. Preserve environment ownership checks, idle batch requirements, active-turn
   branch compatibility (including deferral before an eligible session/environment
   exists), inherited workspaces, and existing source registration
   and credential boundaries. No schema or public API changes.

## Evidence and delivery boundaries

The defect exists in v0.97.0 `e43881c7555372897b57ec51c705f1e05da43c40`
and upstream main `e8c1761cb5b7006b905c0aa995925372ce8664e1`.
Local overlay commits `4f96b6576` and `c687232d2` demonstrate repair intent,
but their `.git` existence heuristic does not establish managed ownership.
Existing managed relocation/session recovery work is reused rather than copied.

Repository regression evidence must cover both entry paths, real Git content,
SQLite readback, exact credential scope, failures and compensation. Unit/service
acceptance does not constitute release/runtime acceptance.

Overlay retirement requires the upstream merged PR and commit, the first shipped
release containing it, and disposable server-in-Docker acceptance for
local/worktree agents on both paths with saved-missing and empty paths. No live
deployment or overlay removal is authorized by this repair task's Work phase.

## Decisions and scope

The approved plan permits valid local repositories outside managed storage and
retention of successful shared clones after task attachment rollback. No plugin
implementation, terminal retention/routing change, repository placement redesign,
new flags or deployment changes belong in this repair.
