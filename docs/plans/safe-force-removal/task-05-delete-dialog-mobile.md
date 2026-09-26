---
id: "05-delete-dialog-mobile"
title: "Delete dialog and mobile interaction"
status: pending
wave: 4
depends_on: ["03-shared-service-http-api"]
plan: "plan.md"
requirements:
  - REQ-TASKS-SAFE-FORCE-REMOVAL-001
  - REQ-TASKS-SAFE-FORCE-REMOVAL-002
system_design:
  - ../../specs/tasks/system-design/safe-force-removal.md
---

# W05: Delete dialog and mobile interaction

Add the single-task force branch to the existing dialog, including preview,
exact UUID confirmation, blocked outcomes, receipt display, and six-locale
copy.

## ASCII UI preview

Use [UI-01](plan.md#ui-preview). Keep the phone body scrollable with fixed,
44px actions; ordinary Delete remains disabled when preflight failed.

## Verification

```bash
(cd apps/web && pnpm exec vitest run components/task/task-delete-confirm-dialog.test.tsx)
(cd apps/web && pnpm run typecheck && pnpm run i18n:check)
(cd apps/web && pnpm e2e:run --project mobile-chrome tests/task/mobile-delete-force-removal.spec.ts)
```
