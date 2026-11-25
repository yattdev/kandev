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
`scripts/kandev-agent-docker-broker`,
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
commit/hash/test evidence. No install, restart or task lookup against live data.

## Results

Deployment-source implementation is complete; see evidence README for its
exact commit, patch and source hashes. `python3 tests/test-task-lifecycle.py`
passed 20 synthetic cases with real fixture SQL, source-pinned launch matching,
kernel identity and Unix transport. The existing broker policy suite passed,
including its 16-test and 11-test subordinate suites. `git diff --check` passed.
Fixture databases and sockets are removed through test cleanup. No live task,
runtime or data access occurred. Development custody and the process-fixture
ancestry boundary are substituted; actual installed validator custody/interface
was separately qualified without metadata calls. No installed-route acceptance
or source-to-live activation is claimed. Exact artifacts accompany this package.
