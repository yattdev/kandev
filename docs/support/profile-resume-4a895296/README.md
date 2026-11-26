# Removed profile blocks preserved-session discovery

Current: [escalation 1 audit](pass1/README.md), which corrects the initial model
comparison with the retained snapshot and exact registered-default evidence.


Request `4a895296-0892-461d-857e-1f06f2fff8c4`.

Status: **BLOCKED on an explicit Human-owned successor-profile decision**, not
on a Docker guard, missing host authority, or an upstream-only platform issue.
Reviewed host Support used bounded read-only configuration metadata to establish
actual bindings. No target plan/source/worktree/PR/conversation/runtime/customer
content, credentials or provider was accessed; no task/session/profile/queue or
lane was changed and no resume/replay/launch/restart occurred.

## Authoritative current configuration

Exact task `5129db6a-0707-421b-b80f-cce4c36a6ad1` belongs to the Coordinator's
workspace `25f53734-5aea-490e-b7ba-d5da6fe821dd`. It remains in PR Review workflow
`4b024728-3ab7-4737-ba14-9c06620ed7c2`, Backlogs step
`a23816ca-20b1-4e25-a504-a41828e8089e`. The task and original primary session
`16bcc779-85b0-45a1-9ab7-46f3668fe410` are FAILED. All five session IDs remain;
the other four are completed, nonprimary.

Stored profile `62434899-7d6b-432d-b93f-0b71c67eb35d` (`AutoPilot Review`) is
explicitly soft-deleted at `2026-11-09T17:51:18.219155611Z`. Enabled=1 does not
undo deletion: enabled controls new-work selection, while deleted profiles are
excluded from resolution. Its agent is `copilot-acp`, model `gpt-6.1-terra`, mode
`https://agentclientprotocol.com/protocol/session-modes#autopilot`.

Current workflow settings, last changed Nov9 at18:17Z, have no workflow-level
profile and no Backlogs profile or event actions. Human promotion stays intact.
The **different Review step** binds the active profile below and auto-starts on
enter. Moving there is a review launch, not inert metadata repair.

| Binding | Current profile | Agent/model | Mode |
| --- | --- | --- | --- |
| Review step | `2653283e-3df4-48d6-8242-774b24b54f38`, Terra 6.1 High | copilot-acp / gpt-6.1-terra | agent |
| Workspace default, Human-QA and Done | `ce9f96f7-4434-4729-b23b-926b4b72f417`, Terra 6.1 Medium | copilot-acp / gpt-6.1-terra | agent |

Both candidates match the deleted profile's agent/model and the compared
permission, role, skill, MCP-selection, passthrough, fallback and exact-model
fields. **Mode and CLI flags differ.** Old stored reasoning effort is `high`;
the Review profile has no explicit value in that settings field and the default
has `medium`. Names are not effective configuration proof. Raw launch flags,
environment, secret references and credentials were not disclosed. There is no
profile-successor mapping in the inspected schema/resolver; `migrated_from`
tracks an agent migration, not a successor-profile grant.

The current Review binding is a concrete candidate for the Human maintainer,
not authority to rewrite a historical session in Backlogs. Choosing the
workspace default silently would also change recorded reasoning configuration.
No active same-agent/same-model profile with identical recorded mode was found
in the bounded configuration lookup. No profile is recreated or undeleted.

## Source route and message audit

Both checkouts were inspected. Primary product source is `cfd6bdf...`; canonical
`upstream/main` was fetched and remotely verified as
`f675a554a5b984cdb6d141c6e9f6a91e50d33694`. A transient shared Git config lock
interrupted a branch switch; index/worktree equality to the exact canonical
revision was verified before completing it without deleting locks or discarding
files. `source-status.json` pins the inspected file hashes.

`StoreProfileResolver.ResolveProfile` rejects the deleted row with typed
`DeletedProfileError`; it does not fall back to a name, a current workflow
profile, a workspace default or a historical snapshot. Resume consumes the
session's stored profile. `session.launch` with resume intent has no inert
profile-rebind behavior. Start-created/workflow-step paths can change binding
as part of launch and workflow routing; they are not a metadata-only remedy.
No supported public metadata-only session-profile rebind was identified in the
inspected handlers/action catalog. `session.recover` is a launch operation;
`fresh_start` additionally changes continuation state. The source's generic
advice to create a replacement session conflicts with this request's explicit
preservation constraint and is not the proposed action.

For the exact original session, the bounded queue-identity lookup returns **no
queued row**. A separate bounded identity-only lookup for23:05–23:07UTC also
finds no `agent_delivery_submissions` row; `task_message_send_operations` is
absent from the installed schema. This does not assert global absence outside
that window. The preserved queue incarnation is
`c6b81ff7-5421-433a-9f68-b53438295e24`, exact-profile generation/revision0/0,
execution profile empty, route generation0/state empty. None was changed.
This is not evidence of delivery or permission to re-send. The
inspected idle message path temporarily records a user message before dispatch
and removes that record if prompt/resume fails. Support did not read message
contents or infer a durable pending-entry ID from the failed call. Keep Root's
original failed instruction-call/audit identity; do not claim it was accepted,
replay it blindly, or fabricate a queue ID.

## Missing input and finite preserving continuation

The Human workflow/profile maintainer must choose either:

1. Explicitly designate existing Review profile
   `2653283e-3df4-48d6-8242-774b24b54f38` as the successor **for this exact existing
   session**, accepting its current mode/launch-option semantics after reviewing
   those differences; or
2. Keep this task/session held and discovery blocked. No alternative model,
   restored deleted profile, new session or promotion is implied.

The workspace default is documented to explain why automatic fallback is unsafe,
not offered as an equivalent replacement. If a different existing profile is
intended, the maintainer must identify its exact ID and authorize its differences;
Support cannot infer that from a display name or model match.

After a concrete successor decision, a **separately bound metadata-only recovery
operation** must be reviewed before any acceptance: recheck exact workspace,
task/session/primary/profile and FAILED state; recheck current profile revision
and complete launch-policy compatibility; prove no live executor/launch; retain
all task/session IDs, worktrees, workflow/step, plans/artifacts, continuation and
queue/audit identities; perform only the supported/CAS-scoped binding and snapshot
repair, including applicable execution-profile/generation invariants. Do not
make a raw agent SQL edit or expose a generic import/rebind route. No such
operation is implemented, exposed or executed in this result. A new backend
capability would require its normal source design/review and preserving exposure;
no lifecycle restart or candidate activation is authorized here.

Root's acceptance is **not authorized yet**. Once the selected binding is
independently repaired and read back, Root binds one exact same-session
instruction-only operation and records whether it was accepted. Because no FIFO
entry is established here, that contract must explicitly say whether it is a
fresh submission linked to the original failure or an authenticated retained
entry; no repeated setup, product review, provider execution or automatic launch
is added to this Support request.

## Source/build/installed status

No product source, binaries, broker, guard or runtime configuration was changed.
No candidate build or installation was attempted. Public installed binary hashes:

- Backend: `6d234280b9f0b757e6a2f12ab7d1708ba79cb3440faed9f0e28a1e6db855bf8b`.
- Agentctl: `933d38c1551698c7bd761710d91d00dd2c30f2adaf992858a771609435aa0917`.

Those hashes match existing deployment provenance records. Source revision and
binary hash are reported separately; a version label is not a new build
attestation. The behavior reported by the actual failed call is consistent with
the inspected deleted-profile resolver.

Focused existing resolver tests were attempted offline against the verified
canonical checkout. They did **not execute** because the pinned ACP SDK
`v0.13.6-0.20261123142417-27925bf88fae` is absent from the selected local module
cache. No dependency/provider/registry request was made and no hook/test was
weakened. This tooling limit is separate from the actual Human-choice blocker.
No synthetic or source result is represented as affected-owner acceptance.

Available reviewed host escalation was used for configuration evidence and
public installed-build metadata; no automatic review rejected an action. The
remaining constraint is the Human-owned profile/mode/launch-option choice and
subsequent preserving same-session binding operation, not an agent-facing
restriction misapplied to host Support. The normal broker returns this result
proactively to the Coordinator; no Human relay or polling is requested.
