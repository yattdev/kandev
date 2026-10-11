---
id: "04-regressions"
title: "Integration regressions"
status: done
plan: "plan.md"
spec: "../../specs/managed-source-attachment/spec.md"
---

# Task 04: Integration regressions

Intent and acceptance: implement the corresponding approved plan packet and
spec behavioral contract, preserving source ownership and session credentials.
Dependencies: 01, 02, 03. Execution: sequential, MAIN sole writer.
Files: apps/backend/internal/backendapp/managed_attachment_service_test.go; affected focused tests.

## Verification

```text
cd apps/backend && go test ./internal/backendapp -count=1
cd apps/backend && go test ./internal/task/service -run 'Test(AttachWorkspaceSources|AddBranchToTask)' -count=1
git diff --check
```

## Results

`cd apps/backend && go test ./internal/backendapp ./internal/repoclone -count=1`: PASS;
backendapp 1405 tests/subtests and one optional skip; repoclone 133 tests/subtests.
`cd apps/backend && go test ./internal/task/service -run 'Test(AttachWorkspaceSources|AddBranchToTask)' -count=1`: PASS.
Final new regression rerun: PASS (task 01 command).
`git diff --check`: PASS. `python3 scripts/lint-spec-files.py --all`: PASS.
Foreign/absent local/clone failure compensation; valid local provider metadata;
permission denial; origin/common-dir/Git redirects; credential denial; failed
persistence/output/cancellation/scope are covered. No deployment or external DB.
Main test artifacts stay task-local and untracked.
