<!-- kandev-system: task/session identity and workflow boundaries preserved -->
# Scoped canonical GitHub PR URL association
Task: 47e42432-9f98-4dd9-9996-bf22c20e95cb
Session: 957752ff-6aca-4832-b790-2467672363dc
Parent: a68df3ae-aaf5-4591-a46d-9d73db62e46d
Parent handoff: owner-local-platform-fixes-20261011-v1
Phase: Work-review-qa. MAIN implementation progressing; independent Review/QA and release retirement evidence pending.
Base: 1bba2c18b2c0142474b5f0f8b7f51ec1af109691.
Branch: feature/scoped-pr-url-association-47e4.
Coordination prompts: @workstep-prompt, @codex-dw-spec.
Plan checklist: STEP 1 understand completed; STEP 2 explore completed; STEP 3 design completed; STEP 4 plan saved and read back; parent handoff queued; Todo move accepted with disposition=deferred (move_id ea452cb7-4532-44ec-a1a0-5912a69f5bd1). Physical turn-end application is pending.

## 1. Problem & Constraints

An attended task parent needs to attach an already-created upstream GitHub PR to its direct child when the child's attached repository is the contributor fork. Retain the attached source repository ID while persisting the provider-confirmed upstream PR identity. Reject unverifiable authority, stale revisions and conflicting associations without changing another task or the provider PR.

Ownership: backend core, spanning trusted MCP admission, provider identity resolution and transactional association persistence. This is a platform capability gap. The Kandev server happens to run in Docker, and its local/worktree agents inherit that environment; none of the identified behavior belongs to a task's Docker executor or an image recipe.

Read-only audit on 2026-10-11:
- GitHub API verified current kdlbs/kandev main as e8c1761cb5b7006b905c0aa995925372ce8664e1. Canonical objects are locally available and were inspected with git show/git grep without fetching, checking out, branching or rewriting files.
- Exact v0.97.0 baseline: e43881c7555372897b57ec51c705f1e05da43c40. The URL-specific legacy tool is absent. Relevant MCP handler/coordinator files are unchanged between that baseline and current main.
- Assigned checkout HEAD is cfd6bdf65a (fork lineage), not canonical main. git status --porcelain is empty. Preserve this checkout and any later user edits; do not implement against this stale lineage by accident. The earlier task description's dirty-checkout warning remains a preservation rule even though this session observed a clean checkout.
- Overlay objects 599658501, 717367b94 and ebaadc302 add a dedicated URL tool, require trusted task/session IDs and check source identity, same-slot canonical URL collision and stale stored head. Overlay lineage through 37b8f8a744 and /data/home/Code/kandev/.local-hotfix/pr-url-association-3896.md were inspected. The workspace-adoption patch changes worktree admission, not this capability.
- Current manage_task_change_request_kandev accepts provider/repository_id/number only. taskChangeLinkCoordinator.link constructs the URL from the attached repository owner/name; a yattdev/kandev attachment therefore asks GitHub for yattdev/kandev/pull/3166 rather than kdlbs/kandev/pull/3166. Supplying pr_url currently fails unknown-field/schema validation. This is the source-level reproduction of the remaining gap, not a runtime test result.
- authorizeTaskChangeRequest checks the trusted principal and target workspace, but does not check direct-parent relationship or live session state. ScopePrincipal verifies session ownership when the task lookup supports it; it does not establish RUNNING state for this mutation.
- AssociateExistingPRByURLForWorkspace already resolves authenticated, workspace-scoped provider access and accepts URLs. Its permissive parser and association writer lack the overlay's strict URL/source/head checks. associatePRWithTaskForSession can return an existing same-slot row without comparing URL or fresh head.
- ListTaskPRsByPRNumber is workspace-scoped, deliberately permits multiple task associations, and can support bounded competing-link inspection. Existing human/manual associations must retain their documented semantics.

Reuse and supersession:
- Follow-up exact overlay evidence (Coordinator 2026-10-11): Support task 7effc837-ea2e-4e6c-98a7-21bfaff702e8 deployed 5c564dbd4ed9d3f3b1cd30e15587300ec8141df0. Read-only Git inspection confirms separate validateGitHubPRLink (open/merged), validateGitHubPRIdentityAndRefs, and validateExistingGitHubPRLink (canonical slot collision/stale active head), with contribution editing still open-only. Existing regression tests are TestVerifiedPRLinkMergedDeliveryAndRetry, TestVerifiedPRLinkRejectsUnsafeMergedResponses, TestVerifiedPRLinkPreservesCollisionAndStaleGuards and TestVerifiedPRLinkDoesNotAdmitMergedContributionEditing.
- Coordinator-reported deployed acceptance: canonical merged https://github.com/kdlbs/kandev/pull/3988 linked once to task prefix 1dc4067e with repository prefix b86ec283 and head f380a2532168c3ea0fde19e5cb10b02382f72263; board readback showed exactly one association. Independent read-only GitHub inspection confirms #3988 is MERGED with that head and merge commit 64f8830d0d7b1ea98da96d3373d68bc0386cf49e; it does not independently verify the deployment's task association. This is reported local-overlay evidence, not this session's independent live acceptance or an upstream release receipt.
- Canonical main was rechecked via GitHub and remains e8c1761cb5b7006b905c0aa995925372ce8664e1. Its tree lacks service_pr_link_validation.go and the separated validator; existing URL linking is still the permissive path. Reuse/adapt the four overlay regressions and pure validator separation within implementation step 2, rather than commissioning a second merged-PR support patch. Do not replay the old tool/API or the full overlay. No scoped plugin Host write exists to substitute for core admission.
- Archived af3d7a12-5fc2-408e-ab36-bb4bba6fed22 produced PR #3506, merged at 262ecdf270b5171538a68e9937dda7894ee01673. Its final plan receipt (2026-09-15) explicitly supersedes the earlier claim that 2ba884ba5/bd92d72aa were unmerged: no recovery delta remains.
- PR #3671 provides the current provider-neutral surface; #3698 restores single-user identity handling; #3885 covers fork PR task launch; #4259 scopes reverse association reads. Reuse all of these. Launch validation and reverse read fixes do not implement this agent write contract.
- GitHub all-state searches for association, PR URL, scoped PR and canonical work found no equivalent strict URL mutation PR. Recheck at implementation start since concurrent work may land.
- Current plugin Host exact APIs expose ListChangeRequestEvidenceExact and task management commands, but no PR association mutation. A plugin cannot implement this safely through read capabilities, direct SQL or generic task field updates. The plugin MCP ADR requires backend authorization and grants; this task adds no plugin process or plugin API. Open related Coordinator Host work (yattdev/kandev PR #17) and provider access PR #3165 are coordination context, not upstream prerequisites to cherry-pick.
- Retention task 3cf0e07b-eac6-4703-bd5d-5d31920bf3e0 (#3930) and silent-routing task 2473291a-614b-418f-9cb1-8b73c0ea7b57 remain independently owned. Do not create duplicate tasks or retire their overlays.

No file writes, Go tests, builds, live links, deployment changes or provider mutations occurred during Spec. Tests/builds would create files, so execution evidence must be collected in Work.

## 2. Acceptance Criteria

AC1. Existing provider-neutral number-based link/unlink/replace calls and GitLab behavior remain compatible. The legacy URL tool name is not restored.

AC2. Extend manage_task_change_request_kandev with optional pr_url and expected_head_sha. They must appear together, only for operation=link and provider=github. Existing required task_id, repository_id and number remain; number must equal the URL's positive number. Reject URL fields for unlink/replace/GitLab, missing pairs, malformed SHA, unexpected fields and injected caller identity before provider access.

AC3. URL mode requires a server-derived task-bound principal with a persisted RUNNING caller session belonging to the caller task, a nonarchived caller and a nonarchived direct child in the same workspace. Self, sibling, ancestor, grandchild, unrelated task, external unbound caller, configuration/automation/managed-conversation surfaces, foreign workspace, mismatched/nonexistent/terminal session and spoofed payload IDs fail closed. Preserve the existing principal as the authority; do not trust a requested session/task identity. Revalidate persisted authority and repository attachment at the write boundary.

AC4. repository_id must be attached to the child and resolve in the same workspace to a GitHub source repository. The URL must be HTTPS github.com/{owner}/{repo}/pull/{positive_number}; forbid userinfo, ports, alternate hosts/schemes, query, fragment, whitespace, encoded separators and extra path segments. Permit a single trailing slash and normalize provider-canonical case. Reject incomplete/unsafe input before fetch.

AC5. Resolve the PR with existing workspace repository policy and authenticated personal-read routing. Provider number, target owner/name, canonical HTML URL, safe head SHA and head source owner/name must agree with the request and attached source. Support same-repository and fork-to-upstream PRs. Never substitute the fork URL for upstream URL, move the attachment, or infer identity from the number alone. Fail on missing source/target identity. Association requires read permission, not maintainer push permission.

AC6. Fresh provider head must equal expected_head_sha, case-normalized. An active same-slot stored association with a different head is rejected, even if the caller supplied the fresh head. A different canonical URL in the same task/repository/number slot is rejected, including a detached tombstone. Failures preserve rows, automation, comparison targets and events.

AC7. A competing non-detached association for the exact workspace/canonical owner/repo/number on another nonarchived task causes URL mode to fail with conflict. Preserve all existing rows; never detach, transfer, replace, repair or archive another task. Do not enforce global PR uniqueness or alter legitimate number-mode/manual multi-task associations. Same-number PRs in different canonical repositories are distinct.

AC8. Valid identical same-task URL/head retry is idempotent: one association, no duplicate created event and no unrelated row change. Restore a matching detached association only after full fresh validation; never restore a conflicting canonical identity.

AC9. Identity, same-slot head/canonical collision, competing-link inspection and insert/restore are fenced in a single database mutation boundary. Two concurrent URL-mode claims cannot both succeed; two conflicting URLs for one slot cannot overwrite each other. Synchronize shared insert/restore writers sufficiently to prevent precheck/write races; an in-memory mutex alone is insufficient. Authority revoked or attachment changed before commit causes rejection. Use SQLite and supported PostgreSQL semantics without a global uniqueness migration.

AC10. Successful response preserves task_id, links and state_known and includes persisted canonical URL, attached repository_id, number and head_sha for the resolved association. Read back the exact persisted row and active set. Readback failure returns state_known=false without fabricated success or automatic retry. Validation conflicts return stable sanitized reasons and known unchanged links when readback succeeds.

AC11. Existing provider status/outcome fields, explicit URL-link source, comparison target reconciliation and WS event semantics are preserved. Events/reconciliation run after the guarded association commit and only for the intended task; do not run side effects for rejected attempts. No PR creation, update, merge, deletion or provider-side write occurs.

AC12. Open and merged PRs with provider-confirmed source identity and matching head may be linked; closed-unmerged PRs are rejected in strict URL mode. Contribution editing remains open-only: accepting a merged deliverable association never admits merged contribution editing or grants branch-write/automatic-launch permission. Merged links retain existing merged outcome attribution. Deleted/missing fork identity fails closed.

AC13. Unit/protocol, real-store integration and concurrency tests cover AC1-12, including rejected attempts leaving other task links and provider mutation counters unchanged. Test failures reproduce the gap before the implementation; source inspection alone is not a test receipt.

AC14. Overlay retirement includes the later merged-PR validator fix 5c564dbd4e as well as the original three URL-link commits. It requires a merged canonical commit, a named release/artifact containing it, and isolated deployed-binary acceptance evidence against that artifact. Record exact SHA, version/image digest, backend/agentctl versions and catalog plus a positive fork/upstream link, denied scope/stale/collision cases, persistence readback and unchanged unrelated links. Passing main-source tests alone does not authorize removal or deployment.

## 3. Approaches Considered

1. Replay the overlay and restore link_task_pr_by_url_kandev. Small apparent diff but duplicates the current tool contract, inherits outdated checks, and omits database concurrency guarantees. Rejected.
2. Extend the provider-neutral core tool with a narrowly scoped URL mode, using current provider routing, association metadata and persistence patterns. Recommended: one backwards-compatible entry point, one core owner and independently testable guards.
3. Implement a Coordinator plugin tool plus a new Host exact change-request mutation capability. Appropriate only if a supported plugin-owned product surface becomes a requirement; today it expands SDK/protobuf/grants/plugin release scope without eliminating required core checks. Deferred. Do not emulate it through generic Host writes.

Plan approach 2. No task executor, image, frontend component or plugin production repository changes.

## 4. Assumptions Made

1. May the URL mode be restricted to direct children only, leaving self association to existing number-mode/human paths? Chosen to match the incident's direct-parent scope and avoid widening authority. Override adds self admission scenarios and tests.
2. May strict URL mode require expected_head_sha while retaining the existing required number? Chosen to reject a stale initial request as well as stale persisted links, and keep the portable schema/backwards compatibility simple. Override changes the schema and initial-link stale-head guarantee.
3. May strict URL mode reject another nonarchived task's non-detached link even though existing manual/number-mode links can legitimately share a PR? Chosen as a local opt-in collision policy matching the requested competing-association rejection. Override changes AC7/concurrency tests, not global association semantics.
4. May strict URL mode support open and merged PRs, rejecting closed-unmerged PRs? Chosen to preserve useful canonical merged-PR receipts supported by current core linking (the parent's latest read-only conversation records a merged #3988 receipt); the old overlay's open-only restriction is superseded. Override narrows state validation and tests.
5. May this delivery stop at core MCP support without introducing a plugin Host association writer? Chosen because core owns the invariant and no equivalent Host write is currently present. Override requires separate SDK/Host design and potentially a second repo/PR with explicitly requested persistent subtask coordination.

Author approved the saved package for implementation on 2026-10-11. The five recommendations are now the authorized implementation scope.

## 5. Author Decisions

The task author requires current upstream architecture reuse, trusted active-session/direct-parent scope, canonical fork/upstream identity, stale/collision rejection and fail-closed unsupported scope. Keep deployment unchanged, inspect canonical Git objects without disturbing the checkout, and distinguish Docker-hosted server behavior from task Docker execution. Save through Kandev plan tools in this read-only phase. No additional RUNG-3 questions were asked.

## 6. File Map

All paths are ordinary files in the Kandev monorepo; none is a submodule.

Create during Work:
- docs/specs/integrations/requirements/github-scoped-pr-url-association.md: observable requirements and GIVEN/WHEN/THEN failure cases, aligned with the current integration spec format.
- docs/specs/integrations/system-design/github-scoped-pr-url-association.md: trusted admission and transaction design.
- docs/decisions/2026-10-11-scoped-pr-url-association.md plus INDEX.md: opt-in strict agent mutation boundary, core ownership and preserved manual multi-link behavior.
- docs/plans/scoped-pr-url-association/plan.md and task-01 through task-06 files: materialize this saved package, pending statuses, exact checks/results. Do not treat their creation as extra implementation authorization.

Modify/extend:
- apps/backend/internal/mcp/handlers/task_change_link.go: URL fields, cross-field validation, trusted direct-parent session admission and structured result. Extract focused helper files to comply with size/complexity limits.
- apps/backend/internal/mcp/handlers/task_target_access.go and scope/principal.go: reuse existing relationship/principal helpers; avoid changing global resolver semantics just for this operation.
- apps/backend/internal/backendapp/task_change_link_coordinator.go: pass URL/head and authenticated scoped identity to a guarded provider seam; reuse attached repository resolution and exact link readback.
- apps/backend/internal/github/service_remote_contribution.go: reuse strict URL parsing/path/ref primitives without importing contribution-launch requirements such as maintainer edit permission or open-only state.
- New internal/github/service_scoped_pr_url.go and store_scoped_pr_url.go: focused fetch/verification and transactional admission/insert/restore. Reuse existing TaskPR schema/outcome helpers.
- apps/backend/internal/github/store.go, store_task_pr_ownership.go and service_pr_watch.go: only shared mutation seams necessary for concurrency fencing and postcommit metadata/event compatibility.
- apps/backend/internal/mcp/server/server.go and handlers.go: portable schema, argument forwarding and local exclusivity checks; no new WebSocket action or legacy tool registration.
- Matching *_test.go files, including new scope/provider/store tests and existing task_change_request_tools_test.go, task_change_request_protocol_test.go, task_change_request_test.go, task_change_link_coordinator_test.go, service_associate_existing_test.go and store_pr_lookup_test.go.
- docs/public/automation-and-mcp.md: usage example, strict mode permission/head/conflict/state-known semantics. coverage.json only if validator requires contract inventory changes.
- Scoped AGENTS.md only if the new invariant warrants a concise convention note.

PROMPT.md is local-only, NEVER staged/committed/pushed/added to a PR. Creation is deferred until file writes are allowed. It must contain the supplied orchestration rules, task/parent/session IDs, canonical base, overlay evidence and sibling context.

Plugin SDK/protobuf, generated stubs, Docker files, workspace-adoption patch and web UI remain out of the file map.

## 7. Implementation Steps

Execute these six packets sequentially after an explicit Work request. MAIN remains the sole writer. The later explicit Work instructions authorize two distinct Kandev side sessions for independent Review and QA; no implementation workers or additional persistent tasks.

1. Re-anchor and materialize the design package.
   Recheck current canonical main, existing PRs and parent/sibling updates; retain source-level vs runtime evidence distinction. Preserve checkout/user edits and arrange the assigned implementation base through the supported workflow. Materialize spec/ADR/plan/task files and local-only PROMPT.md. Add failing protocol/coordinator cases proving URL input is absent and a fork source produces the wrong URL. Owned files: design package plus existing protocol/coordinator tests. Verify: cd apps/backend && go test ./internal/mcp/server ./internal/backendapp -run 'TestManageTaskChangeRequest|TestTaskChangeCoordinator.*URL' -count=1. Record expected pre-fix failures.

2. Provider verification.
   Add a typed guarded association request with source attachment, URL and expected head. Reuse authenticated workspace routing and strict URL/ref helpers; validate provider target/source/head and open-or-merged state before writes. Keep existing permissive human linking unchanged. Reuse the verified pure validator separation and four regression scenarios from overlay 5c564dbd4e; do not add a separate merged-PR fix PR. Include a negative regression proving merged linking never grants contribution editing or launch authority. Owned files: service_scoped_pr_url.go and tests, small provider seam. Verify: cd apps/backend && go test ./internal/github -run 'TestScopedPRURL.*(Identity|URL|Head|State|Scope|Provider)' -count=1. Cover all AC4-6/12 rejection branches and zero provider-side writes.

3. Transactional association admission.
   Implement one store transaction that checks fresh persisted parent/session/attachment scope, canonical slot, stale stored head and other-task competitors before insert/restore. Serialize competing scoped claims using database locks/transaction semantics; fence relevant shared writers, preserve existing outcome and detach metadata and publish only after commit. No global uniqueness constraint. Owned files: store_scoped_pr_url.go/tests and necessary shared store seams. Verify: cd apps/backend && go test -race ./internal/github -run 'TestScopedPRURL.*(Atomic|Concurrent|Collision|Restore|Idempotent|Admission)' -count=1. Add supported PostgreSQL runs against a disposable test DB using the repo's existing environment gate; record the actual gate and execution result, never claim a skipped test as acceptance.

4. Core MCP admission and dispatch.
   Extend the existing schema/request/result and coordinator dispatch. Server performs pair/mode checks; backend independently rejects forged payloads and validates a persisted RUNNING task-bound session and direct child before fetch. Transaction from step 3 revalidates before write. Existing number-mode behavior remains unchanged. Do not expose URL mode as plugin authority. Owned files: MCP handler/server and coordinator seams/tests. Verify: cd apps/backend && go test ./internal/mcp/handlers ./internal/mcp/server ./internal/backendapp -run 'Test.*(TaskChange|ScopedPRURL|Portable)' -count=1. Include external/raw-WS rejection and single-user/multi-user identity routing.

5. Integration coverage and public contract.
   Build a real scoped handler/coordinator/provider/store test using disposable SQLite and a fake provider. Cover fork/upstream success, merged receipt, retries, same-number different URL, stale first request/stored row, other-task collisions, cross-workspace/nonparent/terminal-session rejection, revocation between fetch and commit, simultaneous claims, readback failure and unchanged unrelated links/outcome automation. Update public docs/spec indexes. No UI changes, so browser screenshots/mobile E2E are not necessary. Verify: cd apps/backend && go test -race ./internal/mcp/handlers ./internal/mcp/server ./internal/backendapp ./internal/github -run 'Test.*(ScopedPRURL|TaskChange|AssociateExistingPR|ListTaskPRsByPRNumber)' -count=1. Also run node scripts/validate-public-docs.mjs and node --test scripts/validate-public-docs.test.mjs .github/scripts/pr-docs.test.cjs from repo root. Record outputs; new changes/failures determine any expanded checks.

6. Narrow PR delivery and retirement receipt.
   Recheck equivalent upstream work, update pending/done task results, use local commit/push/PR skills and task-defined checks, and let the parent own scheduling/PR disposition. Do not auto-merge or deploy through this spec. Record canonical PR, accepted head, merged SHA and release containment when available; collect isolated binary acceptance separately with supported task-owned resources and exact catalog/head/state readback. Parent owns local deployment change and removal of only 599658501/717367b94/ebaadc302-equivalent overlay behavior after AC14. The old tool name can disappear only after its callers migrate to the new URL mode. Retention/routing/adoption patches remain separately gated. Results pending.

Each implementation task file must include pending/in_progress/done status, owned files, dependencies, acceptance conditions, exact verification commands, risks and actual result receipts. Never label future work verified.

## 8. Testing Strategy

Unit: table-driven portable schema/mode/URL/head/provider identity/state validation, trusted-principal and direct-parent admission matrix.
Protocol: exercise MCP tool schema and actual forwarding, backend independent validation, existing number/GitLab modes and structured state-known failures.
Integration: handler -> coordinator -> authenticated fake provider -> real store with persisted tasks/sessions/repos; assert exact target/upstream URL/source repository/readback and no unrelated side effects.
Concurrency: two guarded requests plus shared legacy insert/restore interleavings; revoke attachment/parent/session after provider fetch but before commit. Assert one winner or fail-closed outcomes, no overwrite and no event from rejected writes.
Persistence: active and detached rows, same-number/different canonical URLs, stale heads, compatible outcome metadata, deterministic readback failure. SQLite mandatory; supported PostgreSQL behavior tests required if dialect-specific locks/SQL change.
Runtime acceptance: only on an isolated candidate release deployment after source tests and merge; do not use the shared live deployment or current overlay to claim upstream release acceptance.
Frontend/mobile: no new UI; existing association events/projections retain their contract. Add frontend tests only if implementation changes that projection.
Verification results: none executed in Spec. All packet commands above are planned, not passed.

## 9. Submodule Dependencies

None. git submodule status returned no entries; .gitmodules is absent. Canonical main and v0.97 trees contain no gitlink entries. Plugin production repositories are separate repositories, not submodules, and are outside this PR.

## 10. Risks & Open Questions

No unanswered RUNG-3 question.
- Main and plugin/Host work may advance before Work: re-audit instead of replaying the overlay.
- Database transaction fencing must preserve intentional manual multi-links and PostgreSQL support. Prove interleavings; do not replace the guard with process-local locks.
- A provider can move its head after the fresh fetch. expected_head_sha establishes the observed revision, not a provider-side lock; no claim of locking GitHub is permitted. A later mutation must observe/fence anew.
- Missing fork identity on a merged PR requires fail-closed rejection rather than guessing. Do not weaken source validation just to produce a receipt.
- Postcommit event/readback failure may leave a committed association; state_known and retry semantics must make this explicit.
- No release containment or deployed-binary acceptance is yet known. Keep local overlay until recorded evidence satisfies AC14.
- Todo moves from RUNNING sessions may be deferred until turn-end; report accepted/deferred separately from physically applied.
- Requested local-only PROMPT.md and durable repo artifacts cannot be written in Spec; this platform plan is the authorized durable handoff.

## 11. Out of Scope

Task Docker executor bugs, image packages, server rebuild/restart, live deployment mutation, raw Docker exec/socket access, SQLite repair scripts, credentials copying, foreign-task/workspace resources, workspace adoption, terminal retention, silent Done routing, CI retry recovery, generic task authorization overhaul, automatic PR transfer/cleanup, global PR uniqueness, enterprise hosts, GitLab URL association, plugin implementation/Host contract expansion, unrelated archived-task recovery, extra tasks or worker sessions.

Persistent coordination: the primary session owns this plan and preserves user edits using versioned plan tools. Parent is first responder; report progress, branch/dependency changes, readiness and merge evidence to the parent. Keep live infrastructure unchanged and use assigned task resources only. Native delegation requires explicit user authorization. Completion signals are last actions only; no signal during partial implementation or pending questions. No implementation, merge or release acceptance is implied by completing Spec.


## Work verification record

WRQ_STATE: RUNNING. REVIEW_RESULT: PENDING. QA_RESULT: PENDING.
TEST_RUNTIME: TRANSIENT (disposable repository fixtures; no application server or UI change).
UI_VISUAL_CHANGE: NO. Existing link projection is reused; only an agent management API mode is added.
No submodules. No live runtime, provider writes, Docker socket/exec or copied credentials.

Red evidence: new URL/head arguments rejected as unknown by existing server and backend handler; fork coordinator did not forward canonical URL; merged validation/persistence initially unavailable; malformed URL and short-head/mode-pair regressions failed before fixes. Compile/import errors were diagnosed separately and are not behavior evidence.

SQLite targeted four-package race scope/provider/idempotency/full-path suite passed after final behavioral edits. SQL guard, storeconformance race (61.058s), changed-code lint (0 issues), shared writer regressions and documentation validation/tests (164) passed. PostgreSQL real concurrency awaits an approved disposable resource from parent; env-gated test exists. Independent review/QA are not launched until candidate checks complete.

Implementation packet status: 01,02,04,05 source work done; 03 PostgreSQL verification pending; 06 candidate/independent review and delivery pending. No release acceptance or overlay removal authorized by this record.

## Candidate handoff

Code and intended docs are ready for a candidate commit. The source checks above passed; PostgreSQL remains an explicit missing essential verification. No PR or workflow advance is allowed until it and both independent sessions pass. Tests and docs may be committed as a reviewable candidate while that operational resource is pending. MAIN alone fixes findings. Final approval receipts live in platform plan/local artifacts, so recording verdicts does not create another code candidate.

- Fixed during review: none yet.
- Follow-up tasks created: none.
- Action required by author: none; PostgreSQL resource request belongs to parent/Coordinator.
- QA fixes worth reviewer attention: policy is revalidated both before provider reads and within the database transaction.
- Assumptions made during QA: pending independent QA.
- Pre-existing issues found: task GitLab read tool reported provider_read_failed with no linked requests; unrelated to GitHub source work.
- UI screenshots for PR/MR: not applicable (agent API-only change).
- Review artifact location: .kandev/wrq/review. QA artifact location: .kandev/wrq/qa. Both are ignored/local only.
