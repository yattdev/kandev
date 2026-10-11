---
id: "01-admission"
title: "Managed path admission"
status: done
plan: "plan.md"
spec: "../../specs/managed-source-attachment/spec.md"
---

# Task 01: Managed path admission

Intent and acceptance: implement the corresponding approved plan packet and
spec behavioral contract, preserving source ownership and session credentials.
Dependencies: None. Execution: sequential, MAIN sole writer.
Files: apps/backend/internal/repoclone/managed_attachment_path.go; apps/backend/internal/orchestrator/executor/host_repository_materialization.go; adjacent tests.

## Verification

```text
cd apps/backend && go test ./internal/repoclone -count=1
cd apps/backend && go test ./internal/orchestrator/executor -run 'Test.*(EnsureRepositoryCloned|HostRepositoryMaterialization)' -count=1
```

## Results

`cd apps/backend && go test ./internal/repoclone -count=1`: PASS,
133 tests/subtests. Exact-base executor focus: PASS, 15 tests/subtests.
`go test ./internal/orchestrator/executor -run 'Test.*(EnsureRepositoryCloned|HostRepositoryMaterialization|ManagedClone.*)' -count=1`.
After final constant extraction, every new admission/executor regression passed
again with `go test ./internal/repoclone ./internal/backendapp ./internal/orchestrator/executor -run 'Test(ManagedAttachment|ManagedSourceAttachment|HostRepositoryMaterialization)' -count=1`.
Evidence: task-local `.kandev/managed-attachment/main/{packages,executor}.jsonl`.
No dump or credentials. Temporary Git/SQLite fixtures clean up through testing.T.

Compatibility correction: the pre-workspace GitLab owner/name layout is an exact
legacy candidate too. A new absent-path test first failed with ownership mismatch.
The correction admits that registered layout, validates the origin of an existing
checkout, and still clones an absent legacy source into its workspace destination.
`cd apps/backend && go test ./internal/repoclone -count=1`: PASS after correction.
