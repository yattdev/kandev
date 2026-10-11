---
id: "02-source"
title: "Source attachment"
status: done
plan: "plan.md"
spec: "../../specs/managed-source-attachment/spec.md"
---

# Task 02: Source attachment

Intent and acceptance: implement the corresponding approved plan packet and
spec behavioral contract, preserving source ownership and session credentials.
Dependencies: 01. Execution: sequential, MAIN sole writer.
Files: apps/backend/internal/backendapp/workspace_source_materializer.go; host_repository_materialization.go; managed_source_attachment_test.go.

## Verification

```text
cd apps/backend && go test ./internal/backendapp -run 'Test(WorkspaceSourceMaterializer|ManagedSourceAttachment)' -count=1
```

## Results

`cd apps/backend && go test ./internal/backendapp -count=1`: PASS,
1405 tests/subtests, one existing optional test skipped. Source regression and
real service integration passed, including exact source retry and DB inventory.
All new source tests passed again after final code edits in the combined focus
command recorded in task 01. Temporary Git/SQLite only; no live service.
