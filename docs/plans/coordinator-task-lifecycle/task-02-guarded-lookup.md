---
id: "02-guarded-lookup"
title: "Add exact-session Coordinator lifecycle metadata lookup"
status: done
wave: 1
depends_on: []
plan: "plan.md"
spec: "../../specs/tasks/requirements/coordinator-task-lifecycle.md"
---

# Task 02: Guarded metadata lookup

Acceptance:

1. Implement the exact proposed broker command and bounded schema; authenticate
   HMAC/root/cwd, kernel peer ancestry, exact active session/incarnation and
   registered Coordinator before reading a target in its bound workspace.
2. Synthetic source-bound Unix transport tests cover requirement scenarios,
   including same-owner foreign workspace and masked absence. No task/plan/
   session/queue/lease mutation, Docker/provider call or filesystem probe occurs.
3. Missing task means unproven deletion, planless means `PLAN_NOT_FOUND`, and
   storage errors remain distinct. Revalidate caller identity before release.

Files in deployment checkout on `main`:
`scripts/kandev-agent-docker-operations.py`,
new `tests/test-task-lifecycle.py`, command documentation. Read its CLAUDE.md
first. Reuse installed platform-owned launch-validation functions from
`scripts/kandev-test-worker.py`; do not start its service or alter its policies.
Dependencies: none; independent of native plan-error repair. Execution: sequential. Inputs: spec requirements 001,
002, 004 and plan's broker authorization and metadata section.

Verification from deployment root:

```sh
python3 tests/test-task-lifecycle.py
python3 tests/test-agent-docker-broker.py
git diff --check
```

Commit only the requested changes; update the source plan with deployment
commit/hash/test evidence. Install only the per-request module via task 03; no restart or task lookup against live data.

## Results

Final command: `docker kandev task-lifecycle <task-UUID>`. Implementation and
installed qualification are complete. Both source and installed suites pass 22
cases with actual process ancestry, file custody, pinned validator, exact
launch/session/incarnation metadata, Coordinator registration and read-only
SQLite. A private empty-root namespace and copied public interpreter provide
the fixture boundary without substituting authorization functions. Full
wrapper/client/HMAC/socket-peer dispatch and spoofed-payload rejection pass.

Current evidence README binds source/build/install status and test outputs.
Existing image-ID, broker-policy, volume-preservation and task-access regressions
pass. No live target, task/runtime/data access or owner acceptance is performed.
