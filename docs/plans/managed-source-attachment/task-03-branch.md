---
id: "03-branch"
title: "Branch attachment"
status: done
plan: "plan.md"
spec: "../../specs/managed-source-attachment/spec.md"
---

# Task 03: Branch attachment

Intent and acceptance: implement the corresponding approved plan packet and
spec behavioral contract, preserving source ownership and session credentials.
Dependencies: 01, 02. Execution: sequential, MAIN sole writer.
Files: apps/backend/internal/backendapp/branch_materializer.go; main.go; managed_source_attachment_test.go.

## Verification

```text
cd apps/backend && go test ./internal/backendapp -run 'Test(BranchMaterializer|ManagedSourceAttachment)' -count=1
```

## Results

`cd apps/backend && go test ./internal/backendapp -count=1`: PASS.
All new branch tests passed again after final code edits in the combined focus
command recorded in task 01. Direct and nested materializer wiring exercised;
real service response, readable worktree and SQLite inventory verified.
