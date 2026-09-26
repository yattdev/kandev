---
id: "01-contract-and-dependency-sync"
title: "Contract and dependency sync"
status: done
wave: 1
depends_on: []
plan: "plan.md"
requirements:
  - REQ-TASKS-SAFE-FORCE-REMOVAL-001
  - REQ-TASKS-SAFE-FORCE-REMOVAL-002
  - REQ-TASKS-SAFE-FORCE-REMOVAL-003
  - REQ-TASKS-SAFE-FORCE-REMOVAL-004
system_design:
  - ../../specs/tasks/system-design/safe-force-removal.md
---

# W01: Contract and dependency sync

## Summary

Record the source-independent logical-quarantine boundary and verify the
guarded exact-retirement dependency before shared implementation begins.

## In scope

- Task requirement, system design, ADR, plan, and six work orders.
- Exact PR #3937 state/head verification and explicit integration gate.

## Out of scope

- Shared receipt/auth code, paired preview changes, migrations, production
  implementation, physical cleanup, or actions against reproduction tasks.

## Acceptance

1. The package distinguishes single-card quarantine from paired physical
   retirement and states that unknown is never clean.
2. The dependency is recorded with its exact current head and W02 onward are
   blocked pending the merge commit.

## Verification

```bash
gh pr view 3937 --json state,mergedAt,mergeCommit,headRefName
python3 scripts/lint-spec-files.py --all
python3 scripts/list-docs.py validate
git diff --check -- docs/decisions docs/specs docs/plans
```

## Results

On 2026-09-26 PR #3937 was open, unmerged, at
`9fbfa4caa7dbc53c6c5e782460dbeb277e5b0496`. This work order recorded the
independent contract and did not touch its code or duplicate its preview.
