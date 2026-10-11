# CI Fixup prepared-session retry and startup recovery

## 1. Problem & Constraints

Task: 483c2474-220e-4ff1-91ba-f69df564265a. Session: 4151427f-23d9-40a4-9083-f00c96017012. Parent: a68df3ae-aaf5-4591-a46d-9d73db62e46d. Parent handoff: owner-local-platform-fixes-20261011-v1. Status: design complete; implementation and runtime acceptance pending. Preserve @workstep-prompt and @codex-dw-spec lifecycle rules, user edits, task title, and identity.

Repair scope: a workflow replacement destination is durably prepared against an existing environment; PR-derived launch configuration then rejects its unchanged checkout. Recovery must attach and start that exact destination, including after backend restart, rather than allocating a replacement or reviving a conversation that already ran.

Ownership: Kandev core backend orchestration/executor and its task repository. The Kandev server runs inside Docker and local/worktree agents inherit that environment. This is not evidence of a task Docker-executor defect or an image build defect. A plugin cannot safely own core session state, route promotion, or workspace inventory. Deployment packaging and operator adoption are separate owners.

### Source evidence and read-only reproduction
- GitHub API returned current upstream main e8c1761cb5b7006b905c0aa995925372ce8664e1 on 2026-10-11. Inspect this exact object, not this checkout's HEAD (cfd6bdf65a). git status --porcelain was empty during this inspection; do not assume it stays clean or change another owner's checkout.
- Audit baseline: v0.97.0 e43881c7555372897b57ec51c705f1e05da43c40; deployed v0.96 lineage through 37b8f8a744ea57fe212e8a72d7ea879b37809615 plus patches/kandev-v096-workspace-adoption.patch.
- Canonical commits: 33824242c3ae0df9fbadb4e75f828d038489d55e adds checkout-base preservation and exact retry; dbfd1e793cfaa2b41fbd0b7d0c62dd66f507602e adds startup reconciliation; 7bd1b88a966be010162890ffd3e17e750564d756 clears materialization-only PR/contribution fields and rearms failed destinations.
- v0.96 ports: feb9ce1b07ea72aa060579d71769670ccff83ea0, 820ac3dec9aa104b738f68ba353c1981820de751, db79de75233febb93e8b6c35f452b53338bffe4f. 37b8f8a744 adjusts prompt signatures; this is compatibility history, not current-main implementation.
- Read-only control-flow reproduction against e8c1761c completed via git show/grep and an in-memory Python assertion script. Fixture from canonical TestRepoInfosForWorkspaceReuse_PreservesCheckoutIdentityAfterPROpened: ready environment row repo-1, checkout base upstream/main, branch slug upstream-main, worktree wt-1; live PR target main and empty CheckoutBranch.
  1. executor_resume.go resolvePRBaseForLaunch writes info.BaseBranch = resolved.Target.TargetBranch. buildLaunchAgentRequest passes that value through without the canonical WorkspaceBaseBranch normalization.
  2. executor_environment_reuse.go topLevelBranchIdentitySlug selects main. canonicalInventoryMatches cannot match upstream-main. Admission returns ErrWorkspaceReuseUnsafe before lifecycle attach; the route/destination remain prepared.
  3. Repairing only BaseBranch while retaining a qualified PR base targeting main still fails worktree/worktree.go normalizeQualifiedPRBase, or validateRemoteContribution. Manager.Create calls Validate before ReuseRequired. A lifecycle launch failure then goes through handleLaunchFailure and can mark the destination FAILED before any first turn.
  4. workflow_session_target.go loadRecordedWorkflowSessionRoute excludes terminal destinations and permits a fresh fallback. e8c1761c and v0.97 lack workflow_prepared_retry.go and its exact startup sweep.
- This proves source branches, not a Go-test execution or deployed-runtime reproduction. Implementation step 1 must turn it into failing executable coverage on the then-current upstream base before production changes.
- Deployment notes read: /data/home/Code/kandev/.local-hotfix/README.md and patches/kandev-v096-workspace-adoption.md. The cited .local-hotfix/support-ci-session-4013.md was absent from that directory and the inspected overlay Git tree. Do not claim its logs were read. Source reproduction does not depend on live DB or deployment access.

### Existing work / superseded behavior
- #4416, merged at e8c1761c, owns durable runtime-crash delivery and restart recovery. Reuse its recovery fences and prompt delivery; it does not establish retry for a FAILED prepared route without a first turn/runtime.
- #4415 (open) retries session binding after a workspace materializer election. It is an adjacent creating-environment race, not this ready-environment PR identity failure; synchronize executor edits if it lands.
- #3240 (open) handles resumed STARTING execution readiness timeout. It is not failed-before-first-turn attach recovery.
- #3310 (merged), task a3f02302-12fa-4129-8985-116efb8fed66, owns inventory repair. Reuse current canonical inventory admission/attestation. Never use this repair to bypass an actual inventory mismatch.
- #3166 (open) owns terminal routing arbitration. Respect its serialization if landed; do not duplicate that implementation.
- Reuse existing terminal retention task 3cf0e07b-eac6-4703-bd5d-5d31920bf3e0 / #3930, and silent Done routing task 2473291a-614b-418f-9cb1-8b73c0ea7b57. No new terminal tasks.
- The legacy two separate task/session CAS writes and exact English error-only gate are reference evidence, not a wholesale port. Current-main recovery requires one conditional rearm transaction plus service serialization and the existing durable prompt machinery.

Read-only phase forbids repository writes, tests that create files, commits, fetch/checkout, live runtime mutation, upgrades, and deployments. The plan is saved only through Kandev plan tools. Local PROMPT.md and repository spec/task files are deferred to the authorized implementation phase; do not work around the file-write prohibition.

## 2. Acceptance Criteria

AC1. A valid attach-only workflow destination uses the materialized checkout identity even if a verified live PR targets another branch. Comparison/PR target information remains available independently. Single-repo and multi-repo/repeated-repository slots retain exact task-repository identity. Fresh materialization still uses the verified live PR target.

AC2. Attach-only preparation performs no create/recreate/clone/fetch/pull/checkout/reset or remote-contribution setup. Materialization-only qualified PR base and remote contribution requirements do not reject a valid canonical attach. Provider/head identity checks, contribution destination authorization, inventory, worktree integrity and attestation remain enforced.

AC3. Only the current prepared profile replacement route for the latest exact workflow entry can recover its recorded CREATED or specifically eligible FAILED destination. Require matching task, destination, source, operation/entry, step, profile and environment; ready validated environment; source still primary and parked/waiting; destination nonprimary and not passthrough; zero turns and no runtime/execution or existing delivery ownership. Verify zero turns for CREATED as well as FAILED.

AC4. FAILED rearm is permitted only for the bounded pre-first-turn workspace-attach failure lineage. Unknown failure, failed agent turn, existing first turn in any state, cancelled/completed session, archived/retired task, stale entry, changed source/profile/environment, explicit session target, or unsupported entry actions cause no state change, session allocation or first prompt. Legacy exact error “environment preparation failed: base branch does not exist” is recognized only with all route/ownership guards.

AC5. Rearm and route ownership conditions are atomic. Concurrent startup/retry/replayed workflow events produce at most one attach/start owner, exactly the existing destination ID, one first prompt admission, and no new session or worktree. Source end policy/queue transfer and primary promotion happen only after validated attach, through existing route commit logic.

AC6. Backend startup discovers eligible prepared routes after normal reconciliation and watcher/scheduler readiness, retries through the same guarded helper, and joins/cancels its recovery worker on shutdown. Repeated startup is idempotent. Existing durable recovery owns any route that already has a first turn/submission; this feature does not replay it.

AC7. Evidence includes red/green regressions on upstream main, exact candidate SHA, targeted tests and race results, configured independent Review/QA evidence, PR/head/checks, merged upstream commit, release/image identity and isolated acceptance results. Source merge alone does not authorize overlay removal or a live upgrade.

## 3. Approaches Considered

1. Cherry-pick all three canonical commits: quick provenance, but old APIs, long helper, two-write rearm race, insufficient CREATED/turn guard, and current durable-recovery integration make an unchanged port unsuitable.
2. Retry all FAILED sessions or regenerate a destination: small apparent patch, but violates exact-route ownership, first-turn preservation and no-duplicate acceptance. Rejected.
3. Recommended: adapt checkout-base normalization only on validated reuse paths, then add exact prepared-route recovery integrated with current route/prompt ownership. Use the existing repository transaction patterns for conditional rearm and current service locks to serialize retry. Preserve current-main integrity/delivery mechanisms. One focused core PR.

## 4. Assumptions Made

1. May recovery remain internal to workflow replay/startup without a new public retry endpoint or UI? Chosen yes: existing server entry paths can invoke the helper; new public API/copy expands this repair. If overridden, plan an explicit API/UI contract and mobile/E2E/i18n coverage before implementation.
2. May automatic recovery stay limited to the simple profile replacement step with no explicit SessionTarget and only auto_start_agent on enter? Chosen yes, matching the canonical CI Fixup path and avoiding replay of arbitrary entry side effects. If overridden, separately design idempotent entry-action replay; do not silently widen recovery.

## 5. Author Decisions

The author supplied the exact audit/source baselines, ownership triage, failed-before-first-turn/no-duplicate constraints, existing task reuse, narrow upstream PR, overlay retirement evidence, and no live deployment/upgrade. This turn is the read-only Spec phase; save through the task-plan tool and hand off to the dynamically resolved Todo step. No clarification questions were necessary. No independent approval or runtime acceptance is claimed.

## 6. File Map

All paths are in this repository; no SUBMODULE PATH.

- apps/backend/internal/orchestrator/executor/executor_resume.go: capture immutable checkout base before live PR resolution; normalize reuse in prepareResumeRepositorySettings without changing fresh resolution.
- apps/backend/internal/orchestrator/executor/executor_execute.go: normalize repo info copies before building attach launch requests/specs; preserve live PR comparison state and provider guards.
- NEW apps/backend/internal/orchestrator/executor/executor_workspace_reuse_pr_base_test.go: request-building + admission regressions, multi-slot identity and fresh controls.
- apps/backend/internal/worktree/manager_reuse_required_test.go: prove attach-only no Git materialization and still-enforced integrity. Change production worktree validation only if regression demonstrates normalization cannot fully resolve the mismatch; never globally disable PR validation.
- NEW apps/backend/internal/orchestrator/workflow_prepared_retry.go: eligibility/load/attach/promote/start helpers, split to respect Go lint thresholds.
- apps/backend/internal/orchestrator/workflow_session_target.go and event_handlers_workflow.go: invoke exact recovery before terminal fallback for the eligible recorded prepared route; avoid nested/reentrant route locks.
- apps/backend/internal/orchestrator/session_launch.go: integrate only if unified server admission is needed; do not expose a client-controlled resurrection intent.
- apps/backend/internal/orchestrator/service.go: lifecycle-managed startup sweep after event listeners/normal startup reconciliation.
- apps/backend/internal/task/repository/sqlite/session.go, NEW workflow_prepared_rearm.go and workflow_prepared_rearm_test.go: reuse transaction/conditional update patterns; add one narrowly scoped rearm method that atomically checks latest route/transition, source/destination ownership, no turns/runtime/delivery ownership, nonarchived task, and allowed failure before changing destination/task states. No schema migration expected. Place the new helper in its own file, not the already-large session.go.
- NEW apps/backend/internal/orchestrator/workflow_prepared_retry_test.go: real repository/service orchestration, original failure, replay/startup, rejection and race matrix.
- docs/specs/tasks/requirements/additional-session-workspace-reuse.md and system-design/additional-session-workspace-reuse.md: amend checkout-vs-live-target and exact retry contract.
- docs/plans/ci-fixup-prepared-session-recovery/plan.md plus task-01-reproduce.md, task-02-checkout-identity.md, task-03-exact-rearm.md, task-04-startup-recovery.md, task-05-delivery-evidence.md: materialize this approved scope during implementation, with pending/in_progress/done and exact Results.
- PROMPT.md: local only, never stage/commit/push. Carry coordination rules and current task dependencies after writes are allowed.
- docs/public/operations.md: only if documented automatic restart behavior needs amendment; run docs-maintainer assessment. No UI changes are planned.

## 7. Implementation Steps

Five sequential tasks in the primary session; no subagents or additional persistent tasks authorized.

1. Re-anchor and executable reproduction. Re-read plan/user edits, inspect current upstream main and open PRs, detect equivalent landed work, and preserve any dirty source. Materialize spec/task artifacts and local-only coordination context once implementation is explicitly authorized. Build a real test fixture from canonical checkout upstream/main/live target main, recorded source/destination and entry. Show failing request/attach and missing retry/startup behavior on the exact current upstream base. Record failures/output, not only static assertions. Adapt or drop superseded deltas. Stop production edits if the missing path does not reproduce.
2. Checkout identity repair. Capture stored checkout base separately from live PR target. For WorkspaceReuseRequired, copy repoInfos and restore checkout base while excluding only new-checkout qualified base/remote-contribution execution inputs. Apply consistently to new additional-session launch and resume, including all repository slots. Keep comparison target/live PR base, credentials/provider authorization and fresh materialization behavior. Verify requests reach canonical attach without filesystem mutation.
3. Exact rearm and workflow replay. Add internal eligibility helper and transactional conditional rearm; zero turns/runtime/delivery guards apply before all retries. Do not rearm on a broad text match. Use existing session/route serialization, re-read after lock, and current conditional route promotion. Route replay must consume the eligible recorded destination, never allocate a fallback for this failure. Attach with StartAgent=false, then promote/apply source end policy and admit the ordinary workflow prompt through existing durable machinery. On refusal preserve source/route and report recoverable failure; committed/first-turn recovery remains with existing durable logic.
4. Startup reconciliation. After normal restart reconciliation and required listeners/scheduler start, enumerate metadata-bearing tasks through existing repository capabilities. Run the same helper in the existing bounded/joined startup recovery lifecycle, not an untracked goroutine. Test shutdown/cancellation, repeated startup, current-main dynamic-route/provider recovery blocks, restart between rearm/attach/promotion and first prompt, and startup racing workflow replay.
5. Delivery and overlay retirement record. Run task checks, assess docs, synchronize Results, commit/push and propose one upstream PR on the then-current kdlbs/kandev main, title “fix(orchestrator): recover exact prepared workflow sessions”. Parent Coordinator owns scheduling and PR coordination; upstream maintainer owns merge. Follow live combined workflow independent Review and distinct QA at the final SHA before PR phase; do not silently spawn sessions from this plan. Recheck #4415/#3240/#3166 conflicts and do not create equivalent PRs. Record merged commit and first released artifact containing it. Supply an isolated acceptance recipe with server hosted in Docker and local/worktree tasks, separately from executor-Docker tests. Do not modify the live deployment.

Overlay retirement: remove only the CI Fixup source delta represented by feb9ce1b07/820ac3dec9/db79de7523 after provider-verified upstream containment, released image digest/version, and isolated exact-route/checkout/no-duplicate acceptance pass. Other overlay fixes, agentctl binaries and operator workspace-adoption attestation must remain until their own owners prove parity. Do not remove the entire backend overlay merely because this PR merged. Parent records remaining overlay ledger; deployment owner performs any future approved rebuild/upgrade/removal.

## 8. Testing Strategy

No Go tests run in this read-only phase. Implementation is TDD; permanent test writes require the later implementation request.

- Executor unit/integration tests: upstream/main vs main, PR retargeting, qualified cross-repo PR base, remote contribution, nil/empty checkout base fail-closed, one/multiple/repeated repo slots, resume vs additional launch, fresh launch controls, invalid PR/head/provider identity still refused. Count zero workspace materialization calls.
- Real SQLite service integration: exact prepared CREATED and legacy FAILED destination, current route/entry/profile/environment, source retained until attach, destination promotion/source end policy, one admitted first prompt and unchanged session/worktree counts.
- Negative matrix: existing first turn of every relevant state including failed/completed, running row/checkpoint/delivery ownership, wrong destination/source/step/profile/entry/environment, source no longer primary, archived/retired task, cancelled/completed destination, unknown failure, passthrough, explicit target/multiple actions, invalid inventory/adoption attestation.
- Repository concurrency/rollback: one transaction wins, CAS loser does not partially change task/session, latest entry changes reject stale retry, failed attach preserves recoverability. Use PostgreSQL fixture via KANDEV_TEST_POSTGRES_DSN for any dialect-sensitive transactional method; unexecuted/skipped PG is reported, never claimed passed. No live DB.
- Startup/service integration + race: retry and startup overlap, repeated sweep, cancellation/Stop joins, process restart at each durable boundary, no duplicate first turn. Existing runtime-crash recovery is preserved.

Commands during implementation, each rooted explicitly:
1. cd apps/backend && go test -tags fts5 ./internal/orchestrator/executor -run 'Test.*(WorkspaceReuse|ReuseEnvironment|PRBase|PrepareResumeRepositorySettings)' -count=1
2. cd apps/backend && go test -tags fts5 ./internal/worktree -run 'Test.*(ReuseRequired|QualifiedPRBase)' -count=1
3. cd apps/backend && go test -tags fts5 ./internal/task/repository/sqlite -run 'Test.*(PreparedWorkflow|WorkflowSessionRoute)' -count=1
4. cd apps/backend && go test -tags fts5 ./internal/orchestrator -run 'Test.*(PreparedWorkflow|WorkflowSessionTarget|RestartReconciliation)' -count=1
5. cd apps/backend && go test -race -tags fts5 ./internal/orchestrator ./internal/orchestrator/executor ./internal/task/repository/sqlite -run 'Test.*(PreparedWorkflow|WorkflowSessionRoute|WorkspaceReusePRBase)' -count=1
6. cd apps/backend && golangci-lint run ./... --new-from-rev=<recorded-upstream-base-sha> --timeout=5m
7. python3 scripts/lint-spec-files.py --all; python3 scripts/list-docs.py validate; git diff --check (separate commands from repository root).

Choose new test names to match these selectors and record actual discovered counts. Run targeted existing durable restart-recovery tests from #4416 if affected integration requires them; no generic redundant audit. No frontend/E2E requirement for an internal recovery-only patch; if API/UI scope expands, amend plan before coding.

Verification Results: read-only source trace passed. Executable regression red/green, race, PostgreSQL, final-head Review/QA, PR, merge, release and isolated acceptance all pending.

## 9. Submodule Dependencies

None. git submodule status returned no entries; .gitmodules is absent. No relevant path lies inside a submodule.

## 10. Risks & Open Questions

- Immutable task-repository base and canonical materialized slot can diverge through legitimate inventory repair; preserve current attestation/selected-worktree authority and fail closed rather than searching another checkout.
- Current dynamic-profile generations and durable delivery admission are newer than the local patch. Retry must not override an existing provider/recovery block or replay a committed submission.
- Lock order/reentrant workflow admission and shutdown sequencing require race coverage. One conditional state write without atomic turn/runtime/route checks is insufficient.
- Legacy error strings are not trustworthy failure provenance alone. Strict route + no-turn/no-runtime predicates and bounded eligible failures are mandatory.
- Missing support-ci-session-4013.md limits incident-log corroboration; canonical objects support the source diagnosis. Runtime acceptance remains outstanding.
- Upstream may change before execution; re-evaluate equivalent work first. No unanswered architectural/security question is deferred to implementation.

## 11. Out of Scope

Terminal retention and silent Done routing; creating-environment election (#4415); general STARTING runtime recovery (#3240/#4416); arbitrary failed/terminal session revival; generic entry-action replay; public retry UI/API; plugins; agentctl image rebuilds; independent checkout adoption/retirement (owned by task 6a9c10e2-7f7c-4d78-8b5c-579e945ab297); live deployment, upgrade, overlay deletion, credentials, host Docker access, raw exec, new tasks or sessions.

Latest coordination record: source audit/reproduction trace completed; core exact-route approach chosen to preserve existing delivery/inventory fences; five implementation tasks pending. Next action is owner review/explicit implementation request and executable red reproduction on refreshed upstream main. No external blocker or live deployment action is pending in this Spec step.

Handoff receipt (2026-10-11): saved content read back byte-for-byte before handoff. Workflow 90f322ed-2159-424d-96e7-c2ad05668b8e was discovered from this task; Todo a1ffe5cc-d614-45ae-a5aa-afcd89d65557 was resolved from returned workflow steps. Move 75d10f3a-a86f-4f3e-8ef1-e3bd3bc78640 returned disposition=deferred. Subsequent list_tasks readback still reports Spec 2352cf1e-3005-4602-9f4d-41215a5b399a; Todo application cannot be claimed before turn-end. Parent primary ccb04d5d-9fdc-4064-bde8-0caac6039496 received queued handoff, not a verified consumption receipt. Parent/next resume should verify Todo application without duplicate moves or prompts. Spec completion signal is the final action; implementation remains pending.


## Implementation record

Approved for implementation in Work-review-qa by the later user request. MAIN is the sole code writer; two distinct read-only Kandev Review and QA sessions are authorized for the final committed candidate. Initial unrelated cfd6bdf65a is preserved on preservation/ci-fixup-initial-cfd6bdf65a; the task branch was rebased onto verified upstream e8c1761c before executable RED. No submodules.

- [x] [Reproduce](task-01-reproduce.md)
- [x] [Checkout identity](task-02-checkout-identity.md)
- [ ] [Exact rearm](task-03-exact-rearm.md): SQLite green; PostgreSQL receipt pending.
- [x] [Startup recovery](task-04-startup-recovery.md): focused and final race green; independent acceptance is task 05.
- [ ] [Delivery evidence](task-05-delivery-evidence.md): committed candidate/side verdicts before PR-phase routing. Merge/release/image acceptance are later gates.

Meaningful implementation refinement: route promotion after attach also checks current entry/source inside its transaction, closing the demonstrated step-change race. No persistence schema or public contract was introduced. Current upstream refuses PR associations with an empty checkout head; tests use valid head identity and retain that guard.

See [candidate handoff](review-notes.md) for checks, runtime/data scope and outstanding verification.


### Review correction record

Round 1 candidate 897f6c37ab received R1/QA-001 (first-prompt crash boundary), R2 (PostgreSQL lock order) and QA-002 (missing PG concurrency evidence). See review-notes.md. R1 uses an optional internal FirstPromptPending field in existing route metadata, written atomically during exact recovery promotion; no table migration or public retry endpoint. This preserves pending entry intent through primary promotion, source end-policy/binding and turn admission failures. Startup completes only current marked CREATED destinations and never rearms a committed FAILED session or replays an existing turn/submission. Additional startup current-profile rejection closes MAIN-003. R2 remains open pending actual PostgreSQL RED, fix and GREEN. Added file ownership: apps/backend/internal/task/models/models.go; orchestrator/workflow_first_prompt.go; sqlite/workflow_first_prompt.go; sqlite/workflow_prepared_rearm_postgres_test.go. All owned by MAIN. No further side sessions, PR routing or deployment until the complete gate passes.

Correction evidence: Coordinator approval mirrored in task plan a726e270 permits task-local signed PostgreSQL 16+ extraction and synthetic repository fixtures. PG16.15 signature and package SHA256 verified; clean private Unix-socket cluster, no TCP. R2 RED: SQLSTATE55P03 from opposing task-row NOWAIT while real rearm waits at held turn advisory. Narrow turn/delivery/task order correction. Five PostgreSQL tests passed unskipped with count=3, including actual competing turn/delivery and exact promotion/pending-prompt writes. First-round SKIP remains historical evidence, not relabelled. Distinct existing sessions must verify the committed correction.
