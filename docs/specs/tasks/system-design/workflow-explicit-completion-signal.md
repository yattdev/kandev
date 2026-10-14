---
status: current
system: tasks
requirements:
  - REQ-TASKS-WORKFLOW-EXPLICIT-COMPLETION-SIGNAL-001
  - REQ-TASKS-WORKFLOW-EXPLICIT-COMPLETION-SIGNAL-003
  - REQ-TASKS-WORKFLOW-EXPLICIT-COMPLETION-SIGNAL-004
---

# Completion signal recovery system design

## Purpose and boundaries

The tasks system owns completion eligibility and its recovery instructions.
This design defines completion diagnostics and durable Review/QA admission.
Gate policy and immutable turn identity constrain the existing signal claim and transition paths.

## Requirement mapping

| Requirement | Design sections |
| --- | --- |
| REQ-TASKS-WORKFLOW-EXPLICIT-COMPLETION-SIGNAL-001 | Applicability response (`advances`/`note`) |
| REQ-TASKS-WORKFLOW-EXPLICIT-COMPLETION-SIGNAL-003 | Diagnostic response, Recovery instructions, Verification |

## Applicability response (`advances`/`note`)

`accepted:true` means the signal was durably recorded — it does not mean the
step will actually advance. A step whose `auto_advance_requires_signal` is
`false` never reads the recorded signal at turn end, so a caller could accept a
signal that changes nothing and have no way to tell from the response alone
(this is exactly what happened in ISSUE-5: an agent woken on a non-auto-start
step called `step_complete_kandev`, got `accepted:true`, and nothing moved).

`internal/mcp/handlers/handlers.go:handleStepComplete` resolves the calling
turn's step through the existing `h.workflowCtrl.GetStep` call already used
for the stale-turn diagnostic above, and additively sets two response fields
before returning:

- `advances` (bool) — the step's `AutoAdvanceRequiresSignal` value.
- `note` (string) — present only when `advances` is `false`, explaining that
  the step does not advance on a completion signal.

`accepted` is never changed by this — ADR 0015 semantics and existing agent
prompts depend on it staying `true` once the signal is recorded. When the step
lookup itself fails (no workflow controller wired, or the step ID does not
resolve), both `advances` and `note` are omitted entirely rather than defaulted
to a guessed value — a caller must not infer "does not advance" from a lookup
failure that says nothing about the step's actual configuration.

## Diagnostic response

`internal/mcp/handlers/handlers.go:handleStepComplete` compares the launch step
from `stepCompletionLaunchStep` with the task's current workflow step.
The mismatch continues to return `VALIDATION_ERROR` before the signal claim.

The error retains its existing prefix for compatibility. Its text includes both
step IDs and the recovery instruction. This information belongs in the message,
because `internal/mcp/server/server.go:stepCompleteHandler` forwards `err.Error()`.
A details-only change would not reliably reach the agent.

Suggested message:

> workflow step changed before signal was recorded. This turn started in step
> <launch_step_id>. The current step is <current_step_id>. No signal was recorded.
> Retrying in this turn cannot recover. End this turn and ask the user to resume
> this session for the current step. After satisfying that step, signal completion
> from the new turn. Do not move the task solely to bypass this error.

A missing stamp or failed turn read remains a separate internal error.
The message does not promise that a concurrent future move cannot occur.

## Recovery instructions

The registered tool description explains the distinction between a stale-turn
error and `already_signaled`. A duplicate preserves the existing accepted signal.
A stale-turn rejection records nothing and requires a fresh turn.

`internal/sysprompt/sysprompt.go` supplies concise recovery guidance in
`stepCompleteSection` and `officeStepCompleteInstruction`. Gating and mode
visibility stay unchanged. The long explanation resides in the returned error.
This keeps prompt and tool-description size budgets intact.

The agent ends its stale turn with a clear recovery request. A user resumes the
same session on the current step. Normal turn creation stamps that step.
The agent evaluates current-step work before its final completion call.
No tool call silently creates a turn, restamps an old turn, or moves the task.

The existing operator move remains documented as a separate manual decision.
An error does not grant an agent new authority to use `move_task_kandev`.

## Persistence and compatibility

There is no schema change, new response envelope, or new MCP argument.
`workflow_step_id_at_start` remains an immutable historical fact.
The task-at-step conditional signal claim and duplicate handling remain intact.
RUNNING and WAITING_FOR_INPUT sessions both retain the mismatch guard.
Idleness does not prove that work belongs to the new step.

## Verification

Handler tests use the SQLite-backed existing fixtures. They reproduce Review to
Work movement after turn creation, reject repeated stale calls, and accept a
call after a new Work turn starts. Both session states retain the guard.

Server tests exercise error forwarding through the MCP handler. Prompt tests
cover gated task and Office contexts, omission from ungated instructions, and
existing size budgets. These are protocol tests, not browser layout changes.

Handler tests also cover the `advances`/`note` response fields directly: a
signal-gated step returns `advances:true` with no `note`; a non-signal-gated
step returns `advances:false` with a `note`; a step that fails to resolve
omits both fields.

## Related decisions and contracts

- [ADR 0015](../../../decisions/0015-explicit-completion-signal-for-auto-advance.md)
- [Turn stamp design](workflow-task-step-transition-ledger.md)
- [Requirements](../requirements/workflow-explicit-completion-signal.md)

## Implementation plans

- [Issue 3772 recovery package](../../../plans/step-completion-stale-turn-recovery/plan.md)

## Durable gate admission

`WorkflowStep.CompletionGatePolicy` persists `none`, `review`, or `qa` in
`workflow_steps.completion_gate_policy`, and crosses controller, template,
portable export/import and step-event boundaries. Initial creation/import and
migration infer Review/QA from the legacy normalized names only when policy is
absent. Stored policy survives renames; an explicit `none` remains `none`.
Migration fills only missing policies, so replay preserves operator choices.
An unknown nonempty policy requires a verdict rather than bypassing admission.

Turn creation stamps the current task step-transition ledger ID in the same transaction as its launch-step stamp. Admission compares that generation with the current ledger, rejecting an older visit even after the task returns to the same step. Missing generation metadata requires a fresh turn.

Gate prompts include the reserved immutable turn ID. The MCP `turn_id` argument
carries that exact ID, without looking up and substituting a later turn at the
transport boundary. The handler rejects a missing gate turn ID or a supplied
ID different from the latest session turn. The persisted signal includes the
turn ID and verdict. Its claim acquires the session turn-write lock and task-row
lock, then verifies the latest turn and current step before writing. Same-turn
claims remain first-wins; a newer turn may replace an older generation's signal.

Both legacy and engine admission require a matching step, no blockers, PASS,
and the latest session turn with the same immutable launch-step stamp. Missing
turn identity fails closed. These checks also apply to user cancellation and
to gate steps with `auto_advance_requires_signal=false`. The flag retains its
existing behavior for `none` steps. Existing transition, handoff and pending
move machinery remains responsible for side effects after admission.

This chooses durable policy over name-based runtime dispatch, and explicit
calling-turn identity over inferring the caller from the latest database row.
The latter inference cannot distinguish a delayed call from a same-step retry.
The distinction is an ordering boundary, not a new permission to move tasks.

Verification covers renamed Review and QA gates across both completion paths,
cancellation, PASS/absent/BLOCKED/FAILED, stale persisted signals, same-step
handler retries, prompt dispatch, portable projections, restart and SQLite/
PostgreSQL claim parity under the race detector.
