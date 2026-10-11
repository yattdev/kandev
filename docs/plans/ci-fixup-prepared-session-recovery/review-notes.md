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

See task-01 through task-04 for executable RED/GREEN commands and source identity. Focused executor/worktree/orchestrator/SQLite checks passed; final post-code race command `cd apps/backend && go test -trimpath -race -tags fts5 ./internal/orchestrator ./internal/orchestrator/executor ./internal/task/repository/sqlite -run 'Test.*(PreparedWorkflow|WorkflowSessionRoute|WorkspaceReusePRBase)' -count=1 -timeout=120s` passed (1.895s, 1.126s, 2.456s). That first-round PostgreSQL test was env-skipped; correction-round PostgreSQL evidence is recorded below. Specification lint, documentation catalog validation and git diff --check passed. Changed-code lint initially found new rearm complexity and passed after helper extraction; commit hooks recheck the frozen candidate.

## Runtime and data

TEST_RUNTIME: TRANSIENT. Only repository fixtures, real isolated SQLite files and mocked external agent boundaries; no reusable web service, HOST/LAN URL or live DB is required for the source regression. Coordinator-approved synthetic fixtures now run on task-owned PostgreSQL 16.15, signed PGDG packages extracted without installation, private Unix socket only. DSN is ignored and mode 0600. No external dump or credentials are used. No release/image/runtime acceptance is claimed.

## Fixed during review

Round 1 on 897f6c37ab: REVIEW e9b5f63c-e85a-4515-9f40-73fca2a5760f and QA 42e8c340-1830-421a-aab1-d0b180e295ee both FINDINGS. Reports live under ignored task artifacts; no pass is claimed.

- R1 / QA-001: promotion-before-first-prompt crash stranded CREATED destination. Permanent RED reproduced zero turns. Correction records optional pending-first-prompt intent in the same recovery promotion transaction. Only the exact current marked CREATED destination can finish; FAILED committed or existing-turn/submission/generation/recovery owners cannot be replayed. Permanent startup, no-pending-intent and existing-first-turn controls are GREEN.
- MAIN-003: startup needed to resolve the current effective step profile, not trust the recorded profile. RED changed profile incorrectly reached STARTING; correction refuses before rearm. GREEN preserves FAILED destination and no turn.
- R2: original task-to-turn wait conflicted with turn admission. Round-2 turn-to-task correction fixed that direction but Review demonstrated the inverse cycle against runtime admission. Superseded both blocking orders. Recovery retains the managed task barrier and only tries the exact turn/delivery advisory locks; an existing owner returns unchanged and releases the transaction, permitting a later exact retry. This keeps runtime, deletion, turn and delivery fences without changing their unrelated admission paths. Permanent RED proved the previous candidate waited on each held advisory; nonblocking correction is covered by deterministic lock-footprint and actual runtime/state, turn and delivery competition.
- QA-002: five actual PostgreSQL tests passed three repetitions without skips: lock footprint, turn race, delivery race, concurrent exact promotion/pending first prompt, and atomic state changes. The initial fixture lacked its workspace row and failed before the tested behavior; that test fixture was corrected before the R2 RED. Logs and signed-package provenance remain in ignored task artifacts. Independent QA must verify the final candidate.

Correction-round race selector passed all three packages with PostgreSQL configured (1.843s, 1.103s, 5.795s). Changed-code golangci-lint reports 0 issues; specification lint, documentation catalog validation and diff whitespace checks pass.

Round-3 PostgreSQL tests passed all seven top-level tests, including both held advisory boundaries and both actual runtime admission methods, with -race -count=3 (40.883s). The opposing runtime lock-footprint probe also passes. Final focused three-package race selector passed (2.103s, 1.136s, 11.818s), changed-code lint reports 0 issues, and docs/spec/diff checks pass.

Round-2 QA passed a0a76c483e; Review found the remaining R2 runtime cycle. Both finished and are WFI. MAIN corrected it only after the round ended. Reuse the same two sessions for the next committed successor; final-candidate re-verification is still pending.

## Follow-up tasks created

None. Reuse terminal retention 3cf0e07b-eac6-4703-bd5d-5d31920bf3e0, silent Done routing 2473291a-614b-418f-9cb1-8b73c0ea7b57 and workspace adoption 6a9c10e2-7f7c-4d78-8b5c-579e945ab297. Parent notified of route-seam overlap with explicit-spawn owner 6cd04ffd-2351-4b45-9247-583200666444.

## Action required by author

No product decision. Parent approved repository-generated PostgreSQL data and owns upstream scheduling. Merge/release/image acceptance and overlay retirement are later delivery gates; no live deployment or whole-overlay removal is authorized.

## QA fixes worth reviewer attention

Atomic rearm and promotion checks; no first turn in either CREATED or FAILED states; repeated/concurrent startup; cancellation and newer workflow entry during attach.

## Assumptions made during QA

Independent QA determines whether essential verification is complete. Two approved assumptions remain: internal retry only, and simple profile replacement steps only.

## Pre-existing issues found

Current main rejects PR associations without a checkout head, superseding part of the old source trace. This repair preserves that validation. Missing support-ci-session-4013.md is documented; canonical source objects were inspected.

## UI screenshots for PR/MR

No UI markup, browser handler, public API, copy or interaction is added. Core recovery restores existing workflow behavior; the source-level regression is exercised through the real repository/service/turn pipeline. No screenshot or deployed-runtime acceptance is claimed. QA must independently record UI applicability and any essential omitted evidence.
