# Lifecycle lookup: escalation pass 1

Request `5e629b49-3b50-472f-85d6-54a8382fa515`.

The prior restart blocker was too broad. The existing broker forwards unknown
operations to the operations module on every request and retains the original
socket stream. The reviewed implementation uses that existing path and kernel
peer pidfd; it does not change the long-running broker or grant another actor
access. The command is `docker kandev task-lifecycle <task-UUID>`.

The dormant source-only `workspace task-lifecycle` prototype is removed from
the broker. Its source now matches installed SHA256 `9514b692d9833fcc64c1978b7553c181c37e84625e645a0ad0521d4b2f298e91`.

[The contract](../coordinator-task-lifecycle.md) documents bounded metadata,
errors, absent-plan typing, masked cross-workspace absence and deletion
uncertainty. Native MCP `get_task_plan` error mapping remains unchanged.

## Review and preservation

`reviewed.json` pins the candidate, current installed base, dependencies and
acceptance tests. `preserving-superset.json` verifies every existing handler and
operations method against the installed source baseline, with only the help
string and new top-level dispatch branch changing. This preserves the previous
image-ID repair (deployment commit `46867816144ee689b310b710cdfddb4ee36e88e9`),
worker and volume routes. No binary overlay is replaced.

Source lifecycle tests pass 22 cases with actual caller-validation, custody,
process ancestry, Coordinator proof, wrapper/client/HMAC/Unix peer and SQLite
fixture reads. Fixture namespaces hide live task data/runtime; the synthetic
backend is a copied public interpreter. There are no authorization mocks and
no real engine, provider or live task operations. Source regressions pass:
18 image cases, 24 preservation cases, existing 16+11 broker cases and transport,
and task-access checks. Installed acceptance must use the same isolated fixture
against installed script bytes, not Root's task identity or target IDs.

## Exposure

The existing operator installer accepts an explicitly selected reviewed manifest:

```sh
python3 scripts/install-task-image-inspect.py --review docs/task-lifecycle-pass1/reviewed.json
```

It requires deployment `main`, exact source/test hashes, exact installed base
and broker/wrapper/client/guard hashes, stable root-owned files, a private backup
and atomic replacement of only the operations module. It checks unchanged
container identity. `installation.json` records the actual operation; installed
fixture outputs and final readback are recorded separately. No server restart,
Kandev image deployment, board mutation or owner acceptance is part of it.

Root then binds one read in its unchanged session/workspace. Support never runs
a live target lookup or 27-record census. G1 remains Root's reconciliation.

## Installed result

Initial route implementation source commit `f7f74059804d28af0aff1a9b428273f26525fe5c`.
Operations SHA256 is `d176a1794af1f5138a9b77d3594f09b9063277f6ea2183877891bb49c7abe7e4`.
The original broker/client/wrapper/guard and pinned validator hashes match the
review manifest. All 22 installed lifecycle cases and 18 installed image cases
pass. The unchanged broker dispatches the new module on each request; the full
installed fixture tests that actual dispatch with its private metadata and
kernel-authenticated caller. This is no claim of affected-owner acceptance.

Container identity and start time are unchanged across the install. The current
start time observed before and after is `2026-11-25T20:31:11.237656793Z`, with
restart count zero at final readback. This supersedes the earlier historical
November 18 observation; Support performed no restart. No image or binaries
were built, deployed or replaced. The result manifest records all evidence.

Final review also tests loss of private fixture metadata after successful caller
authorization. It returns typed `METADATA_UNAVAILABLE`, preserving uncertainty.
The first installed revision passed the same 22 cases; `initial-*` receipts
retain its exact provenance. Final receipts bind the subsequent one-line error
mapping correction and expanded assertion within the storage-fault case.

Final installed correction source commit `b8d4e3db1e826f7867fc2403d157c58b1fa4e57e`; installed
operations SHA256 `d176a1794af1f5138a9b77d3594f09b9063277f6ea2183877891bb49c7abe7e4`. Final 22 lifecycle and 18 image cases pass.
