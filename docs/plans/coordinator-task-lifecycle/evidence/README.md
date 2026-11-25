# Support 5e629b49: installed guarded lifecycle lookup

Request `5e629b49-3b50-472f-85d6-54a8382fa515`, escalation pass 1.
**The restart blocker is resolved.** A restart-free route is installed and
qualified with private synthetic fixtures. Support has read zero live target
records and has not operated the board or reconciled the 27 IDs.

## Available route

```sh
docker kandev task-lifecycle <task-UUID>
```

Use the existing authenticated Coordinator root/session/workspace and one UUID
bound separately by Root. No identity overrides or new credentials are needed.
The exact active launch and registered Coordinator workspace are derived from
the kernel socket peer, peer pidfd and existing pinned validator. Missing or
foreign targets return identical `TASK_NOT_FOUND_OR_INACCESSIBLE` JSON with
`deletion_evidence: unproven`. Visible tasks return active/archived metadata and
plan presence; absent plan code is `PLAN_NOT_FOUND`. No content or runtime is
returned. [Full contract](pass1/contract.md).

## Corrected diagnosis and exposure

The first prototype changed long-running broker dispatch and would have needed
a restart. The unchanged installed broker already forwards unknown operations
to a per-request `runpy.run_path` module with its original `FramedInput` stream.
The corrected route uses that path, `SO_PEERCRED` and `SO_PEERPIDFD`; payload
PID/session/workspace fields cannot establish caller identity. It retains all
existing caller/session/Coordinator/target-workspace checks and masks foreign
rows. The dormant source prototype is removed, restoring source broker bytes
to the unchanged installed broker hash. No restart or trust-grant change occurs.

Deployment source `f7f7405` implements the route; `b8d4e3d` preserves typed
storage failure if metadata disappears after authorization. Final evidence
commit `0517d10` records installation and installed tests. The intermediate
revision and receipts are preserved as `initial-*`. The installed operations
SHA256 is `d176a1794af1f5138a9b77d3594f09b9063277f6ea2183877891bb49c7abe7e4`,
after initial revision `83f8461d32ab08c30ab221451d8904bc6fdc669eb57e45b03915229a34c6c3fa`
replaced `b0e635bd1b3eb8091845d814cfae159f77764ba5b68d68c8401057e1d41a76bc`.
The latter includes image fix `46867816144ee689b310b710cdfddb4ee36e88e9`;
[the preserving-superset check](pass1/preserving-superset.json) confirms all
previous handlers remain intact. No backend, agentctl, helper, image or binary
overlay was replaced. [Review](pass1/reviewed.json),
[installation receipt](pass1/installation.json),
[public file hashes/custody](pass1/installed-public-files.json) and
[result manifest](pass1/result.json) bind the exact bytes and effect.

The original broker, wrapper, client and guard hashes are unchanged. Container
identity, image and start time are unchanged across this operation. The start
time observed before and after is `2026-11-25T20:31:11.237656793Z`; final restart
count is zero. The earlier November 18 observation was historical and is not a
claim about this activation interval. Support performed no service restart.

## Qualification

All 22 source and all 22 installed lifecycle cases pass. These execute actual
wrapper/client/HMAC/socket dispatch, process ancestry, file custody, pinned
validator, active launch/session/incarnation matching, Coordinator registration
and read-only SQLite lookup with **zero authorization mocks**. An empty-root
user/network namespace hides live `/data`, `/run` and `/app`. A copied public
interpreter supplies the fixture backend ancestry boundary. Only private
synthetic records are read; engine dispatch is an assertion of zero calls.
Tests include archived retention, planless/present, masked absence and both
foreign-workspace cases, ordinary/revoked/ambiguous/mismatched callers, exited
original-peer pidfd, PID/incarnation changes, spoofed payload fields, storage,
audit and response bounds. Before/after fixture dumps prove preservation.

[Source output](pass1/source-lifecycle-tests.txt),
[installed output](pass1/installed-lifecycle-tests.txt).
The prior image fix also passes all 18 cases in source and installed modes.
Volume preservation passes 24 cases; broker policy passes its 16+11 cases and
real transport; task-access checks pass. These are synthetic acceptance, not an
affected-owner operation or G1 reconciliation. No live target lookup, provider
call, task/session change, new lease or resource activation occurs.

## Publication and remaining boundaries

[Exact deployment repair](pass1/deployment-repair.patch) is a zero-context
binary-safe Git patch from deployment baseline
`08061e2716c8a3367a99590656656197dedc362d` to `0517d10`, limited to this request.
Apply/check with `git apply --unidiff-zero`. That source baseline includes the
dormant first prototype and installed image fix; broker-source removal restores
already-installed bytes, it is not a live broker replacement. Deployment main
contains unrelated unpublished history; it is preserved rather than pushed.
This source branch publishes the exact requested patch and receipts instead.

Canonical product base remains verified `upstream/main`
`8c4fd38852700a68272502cef39f46d122e7397c`; the original fetch preceded creation
of this existing isolated worktree. Both primary checkouts and unrelated files
remain untouched. Product Go and native MCP `get_task_plan` error mapping are
unchanged. The new lifecycle route supplies the requested reusable metadata
and typed missing-plan alternative. Task 01 remains an optional native-reader
follow-up, separately designed. [Earlier characterization and first-pass
status](first-pass-README.md) are historical evidence, not current activation
instructions. Raw old `INTERNAL_ERROR` responses remain uncertainty.

Finite next action: Root binds **one** `docker kandev task-lifecycle <exact UUID>`
read in its unchanged Coordinator session/workspace. Root owns acceptance and
any G1 reconciliation. No census, polling, moves, archive/delete actions or new
approval request is part of this result.
