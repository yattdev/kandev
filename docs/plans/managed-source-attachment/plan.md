---
status: in_progress
created: 2026-10-11
updated: 2026-10-11
owner: Kandev
---

# Managed source attachment repair plan

Approved tool plan: `f45d0a0a-759f-4bff-a5b6-52007ef85e59`.
Spec: [managed source attachment](../../specs/managed-source-attachment/spec.md).
Base: upstream/main `e8c1761cb5b7006b905c0aa995925372ce8664e1`.
MAIN owns all implementation. Independent Review and QA inspect a committed
candidate in two distinct Kandev side sessions. No implementation delegation.

## Sequential work packets

- [x] [01: Managed path admission](task-01-admission.md)
- [x] [02: Source attachment](task-02-source.md)
- [x] [03: Branch attachment](task-03-branch.md)
- [x] [04: Integration regressions](task-04-regressions.md)
- [ ] [05: Independent verification and delivery](task-05-delivery.md)

Use the existing path-returning session cloner; source and branch materializers
must never construct credentials or infer ownership from `.git` existence alone.
The cloner derives exact workspace and compatible legacy paths. Foreign paths,
unsafe ancestry, occupied directories, Git redirects and wrong origins fail
before materialization. The executor validates clone output before path
persistence. Local repositories retain their separate registration contract.

## Verification results

Red: source/saved-missing failed resolving absent root; branch/saved-missing
failed as non-Git; branch/empty returned no worktree without error. Executor
returned stale/foreign paths and bypassed clone/auth failures. Redirected
`core.worktree` and symlinked `.git/objects` were admitted before the additional
ownership checks. Cloning without a session was admitted before the scope guard.

Green task checks: passed, exact commands/outcomes in task files 01-04.
Independent Review and QA verdicts: pending on the committed candidate. Tests use existing repository fixtures: disposable
SQLite and real Git repositories, no main/shared database or external secrets.
A local Git transport keeps real service/executor/cloner integration offline;
a separate credential denial test verifies exact broker request scope.

## Delivery and retirement

Merged PR/commit: pending. First containing release: pending.
Server-in-Docker runtime acceptance: pending, separate from task Docker-executor
coverage and package tests. Local overlay removal: not authorized.
Independent review and QA must both cover the final committed SHA before the
Work-review-qa handoff. PR creation and CI handling occur in the next phase.

## Submodules and risk

None. Existing upstream recovery fixes are compatibility context, not proof of
attachment behavior. Keep atomic rollback and inherited environment admission.
No new public contract, migration or architectural decision is required.
