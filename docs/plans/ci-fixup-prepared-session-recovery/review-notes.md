# Prepared workflow recovery candidate handoff

MAIN task 483c2474-220e-4ff1-91ba-f69df564265a, session 4151427f-23d9-40a4-9083-f00c96017012.
Branch feature/fix-ci-fixup-prepare-h9x. Base upstream/main e8c1761cb5b7006b905c0aa995925372ce8664e1.
Approved task plan e637629d; final candidate and verdicts are recorded in task metadata to avoid an approval-note commit loop.

## Implemented behavior

Reuse configuration retains the stored checkout base and omits new-checkout PR-base/contribution execution inputs while preserving live comparisons and fresh materialization validation. Exact prepared profile destinations are conditionally rearmed only before any turn/runtime/delivery ownership exists. Workspace attach precedes transactional route/entry/source promotion. Startup uses the joined lifecycle sweep and serializes through first-prompt admission. Existing kept-session routes remain on their ordinary path.

## Fixed during implementation

- Parallel startup previously attempted another launch. First-prompt admission now runs under the same retry serialization.
- Extra entry actions and in-memory runtimes now refuse recovery without allocating a successor.
- A newer workflow transition during attach now refuses promotion inside the primary/route transaction.
- Recovery does not intercept an ordinary kept-session route whose source and destination are identical.
- Resume returns verified contribution identity to the credential broker while only preparation receives normalized copies. A regression caught loss of fork credential scope in an intermediate implementation.

## Verification

See task-01 through task-04 for executable RED/GREEN commands and source identity. Focused executor/worktree/orchestrator/SQLite checks passed; final post-code race command `cd apps/backend && go test -trimpath -race -tags fts5 ./internal/orchestrator ./internal/orchestrator/executor ./internal/task/repository/sqlite -run 'Test.*(PreparedWorkflow|WorkflowSessionRoute|WorkspaceReusePRBase)' -count=1 -timeout=120s` passed (1.895s, 1.126s, 2.456s). PostgreSQL test was env-skipped, not passed. Specification lint, documentation catalog validation and git diff --check passed. Changed-code lint initially found new rearm complexity and passed after helper extraction; commit hooks recheck the frozen candidate.

## Runtime and data

TEST_RUNTIME: TRANSIENT. Only repository fixtures, real isolated SQLite files and mocked external agent boundaries; no reusable web service, HOST/LAN URL or live DB is required for the source regression. PostgreSQL parity is env-gated; approved isolated fixture requested from parent, not yet delivered. No release/image/runtime acceptance is claimed.

## Fixed during review

Pending independent sessions. MAIN alone implements corrections.

## Follow-up tasks created

None. Reuse terminal retention 3cf0e07b-eac6-4703-bd5d-5d31920bf3e0, silent Done routing 2473291a-614b-418f-9cb1-8b73c0ea7b57 and workspace adoption 6a9c10e2-7f7c-4d78-8b5c-579e945ab297. Parent notified of route-seam overlap with explicit-spawn owner 6cd04ffd-2351-4b45-9247-583200666444.

## Action required by author

No product decision. Parent owns PostgreSQL fixture receipt and upstream scheduling. Merge/release/image acceptance and overlay retirement are later delivery gates; no live deployment or whole-overlay removal is authorized.

## QA fixes worth reviewer attention

Atomic rearm and promotion checks; no first turn in either CREATED or FAILED states; repeated/concurrent startup; cancellation and newer workflow entry during attach.

## Assumptions made during QA

Independent QA determines whether essential verification is complete. Two approved assumptions remain: internal retry only, and simple profile replacement steps only.

## Pre-existing issues found

Current main rejects PR associations without a checkout head, superseding part of the old source trace. This repair preserves that validation. Missing support-ci-session-4013.md is documented; canonical source objects were inspected.

## UI screenshots for PR/MR

No UI markup, browser handler, public API, copy or interaction is added. Core recovery restores existing workflow behavior; the source-level regression is exercised through the real repository/service/turn pipeline. No screenshot or deployed-runtime acceptance is claimed. QA must independently record UI applicability and any essential omitted evidence.
