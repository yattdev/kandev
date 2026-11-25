---
id: "03-exposure"
title: "Expose the reviewed lookup without task lifecycle changes"
status: done
wave: 3
depends_on: ["02-guarded-lookup"]
plan: "plan.md"
spec: "../../specs/tasks/requirements/coordinator-task-lifecycle.md"
---

# Task 03: Preserving exposure without restart

Acceptance:

1. Identify the exact current module and preserve all existing handlers and
   installed image-ID fix as a tested superset. Pin dependencies and tests.
2. Reuse existing per-request operations loader and original socket stream;
   do not change long-running broker dispatch, caller trust or service state.
3. Atomically install only the root-owned operations module with a private
   backup through the existing hash-gated operator installer.
4. Qualify installed bytes using the same private synthetic caller/metadata
   fixtures; send Root one-task recipe without doing live acceptance for it.

Files: deployment installer manifest/receipt, installed fixture outputs, exact
source patch and this evidence package. Dependencies: task 02. Execution is
sequential. No image deployment, server restart or task lifecycle is authorized.

## Results

Done. The initial blocker incorrectly treated hot-module exposure as requiring
broker restart; unchanged dispatch already supplies the original kernel socket.
The new direct route uses its pidfd plus existing exact caller validation.
The source-only broker prototype is removed to restore installed broker bytes.

Deployment source `f7f7405` plus final storage-error correction `b8d4e3d`;
final installed evidence `0517d10`. Installed operations SHA256
`d176a1794af1f5138a9b77d3594f09b9063277f6ea2183877891bb49c7abe7e4`.
The previous image-ID fix is preserved; all existing handlers compare unchanged
apart from discovery help and new dispatch. Both source and installed lifecycle
suites pass 22 cases, and the installed image regression passes 18 cases.
Review pins, receipt, exact public file custody and unchanged container identity
are in the evidence package. No image/binary build, restart, new trust principal,
provider call, live target lookup, board mutation or owner acceptance occurred.

Root's one separately bound live read is the finite next action and remains
outside Support's synthetic exposure qualification. It is not an implementation
blocker and is not claimed complete by this task.
