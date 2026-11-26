# Escalation 1: retained session model and overlooked default route

Request `4a895296-0892-461d-857e-1f06f2fff8c4` remains **BLOCKED on an actual
successor/model intent decision**. The first pass's model comparison was
incomplete: it compared the current mutable deleted-profile row to the current
Review profile, without checking the retained session snapshot or task default.

Reviewed host Support authority was used for bounded configuration reads in both
passes. No agent-facing filesystem restriction was treated as a host diagnostic
barrier. Both deployment and platform source checkouts were inspected. No
upstream-only limitation or automatic-review denial is claimed.

## New exact configuration evidence

The same task/session remain FAILED and primary, unchanged since the original
23:06 failure. Backlogs and its no-auto-start configuration are preserved. The
session keeps its original queue incarnation, route identity and exact-profile
generation/revision. No snapshot, token, queue, plan, worktree, PR or artifact is
modified; no runtime/model/provider probe or execution occurs.

| Candidate/evidence | Agent | Model | Disposition |
| --- | --- | --- | --- |
| Retained original session snapshot | copilot-acp | gpt-5.3-codex | Historical retained binding; not a live model probe |
| Removed AutoPilot Review profile's current row | copilot-acp | gpt-6.1-terra | Still explicitly soft-deleted |
| Current Review binding `2653283e-3df4-48d6-8242-774b24b54f38` | copilot-acp | gpt-6.1-terra | Existing Human-configured candidate, not proven same-session/model replacement |
| Task registered default `c06ad00e-0da1-429a-8174-54f97164a289` (`Worker`) | codex-acp | gpt-6-sol | Active, but changes provider family and model |

The bounded same-agent/snapshot-model active-profile query returns no rows.
That is a configured-profile lookup, not evidence that a provider could/could
not still serve the model. No provider contact, package/model query, native-state
inspection or fallback is attempted.

The **file/tool/URL allow flags are identical** between the deleted profile row
and current Review row. The actual flag difference is `--no-ask-user`: enabled
on the removed profile, disabled on the current one. The Copilot registry source
identifies this as disabling/enabling the native ask_user tool. This is not a
new host/filesystem privilege grant, and the prior pass should not treat all
CLI-array differences as unspecified privilege changes. Mode still changes
from autopilot to agent, and stored reasoning configuration changes from high
to no explicit setting. No effective provider-side defaults are inferred.

The session snapshot's historical model does not prove its most recent native
model. Equally, matching two current mutable profile rows does not prove that
rebinding the retained conversation preserves the model. Resolving that intent
requires the existing instruction/profile owner; it cannot be manufactured from
an alias, profile name, unsupported model override or an unremoved snapshot.

## Source semantics and unavailable shortcuts

The inspected resolver deliberately rejects soft-deleted profiles and never
reconstructs one from a session snapshot. Resume names the stored session
profile; launch/startup resolves the selected profile's current model, mode and
configuration. Profiles' native ACP continuation identifiers are not a reason
to substitute another agent family. The task default and workspace default are
not resume-time successor grants.

No compatible public metadata-only profile-rebind route was identified. Existing
start-created, workflow-step and recovery paths can launch, reroute, clear state
or replace a session; they cannot be used as inert probes under this contract.
A direct model/flag override or temporary restoration of the removed profile
would bypass the deliberate deletion or make the reserved selection ourselves.
Those routes are excluded, not unsuccessfully attempted.

The source checkouts and exact inspected file hashes are pinned in
`source-status.json`. No product implementation, binary build, hotfix overlay,
installation or lifecycle candidate activation is made. Existing focused package
tests were previously blocked before execution by the absent pinned SDK in the
selected offline cache. That is **not** this result's authority blocker, and no
new package traffic is needed to keep the metadata diagnosis truthful.

## Exact missing owner/input and next step

The existing Human maintainer of this workspace's PR Review workflow/profile
bindings must settle one question for original session
`16bcc779-85b0-45a1-9ab7-46f3668fe410`:

- Designate existing current Review profile
  `2653283e-3df4-48d6-8242-774b24b54f38` and its configured `gpt-6.1-terra`/agent
  behavior as the intended successor, explicitly resolving the retained
  `gpt-5.3-codex` snapshot and interaction/configuration differences; or
- Preserve the present hold. No currently configured exact snapshot-agent/model
  replacement was found. Do not create/restore a profile or choose Worker/default
  merely to make resume succeed.

No new approval is requested for the bootstrap/discovery objective. The reserved
input is the **profile/model/interaction identity for this old conversation**.
The original request explicitly permits returning a genuine Human-owned profile
choice instead of silently changing it.

After that decision, a separately reviewed same-session metadata-only repair must
bind the exact existing workspace/task/session, stopped FAILED state, selected
profile revision, primary flag and continuation/queue/generation vector. It must
preserve task state, lane, Human promotion, session IDs, continuation and message
audit and must not resume or drain anything. No generic agent SQL/rebind grant is
proposed. Root's ONE independent instruction acceptance comes only after that
repair and readback; no owner retry is authorized by this diagnostic result.

This is a proactive broker result, with no task message, board operation or
Human relay performed by Support.
