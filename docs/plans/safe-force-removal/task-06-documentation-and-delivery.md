---
id: "06-documentation-and-delivery"
title: "Documentation and delivery"
status: pending
wave: 5
depends_on: ["04-agent-tool-surface", "05-delete-dialog-mobile"]
plan: "plan.md"
requirements:
  - REQ-TASKS-SAFE-FORCE-REMOVAL-001
  - REQ-TASKS-SAFE-FORCE-REMOVAL-002
  - REQ-TASKS-SAFE-FORCE-REMOVAL-003
  - REQ-TASKS-SAFE-FORCE-REMOVAL-004
system_design:
  - ../../specs/tasks/system-design/safe-force-removal.md
---

# W06: Documentation and delivery

Document logical removal and receipt recovery, run task-defined checks, obtain
independent review and QA, open a draft PR, and wait for exact-head CI before
requesting readiness.

## Verification

```bash
python3 scripts/lint-spec-files.py --all
python3 scripts/list-docs.py validate
git diff --check
```
