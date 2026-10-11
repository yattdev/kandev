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

## Fixed during independent review

R1: the first committed candidate admitted symlinks below `.git/refs`. A reviewer
probe proved that ordinary Git worktree creation wrote a branch ref to a foreign
fixture directory. MAIN added failing nested ref/object/worktree metadata tests
and real service tests for both attachment operations; both service routes first
attached the redirected source successfully, contrary to the required rejection.

Admission now traverses the Git metadata tree without following symlinks and
propagates traversal errors. Real service regressions assert attachment and
inventory compensation plus a byte-identical foreign ref-directory snapshot.
The correction does not remove or rewrite the unsafe checkout. Tests also now
assert that a valid managed clone and repeated repair avoid additional clone
requests. Final independent verdicts belong in task-plan metadata and remain
pending until both sessions verify the corrected committed SHA.

Correction checks:
`cd apps/backend && go test ./internal/repoclone ./internal/backendapp ./internal/orchestrator/executor -run 'Test(ManagedAttachment|ManagedSourceAttachment|HostRepositoryMaterialization)' -count=1`: PASS.
`cd apps/backend && go test ./internal/repoclone ./internal/orchestrator/executor -run 'Test(ManagedAttachmentPathAdmission|HostRepositoryMaterialization)' -count=1`: PASS.
