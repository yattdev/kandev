# Final escalation: effective configuration versus native CLI policy

Request `4a895296-0892-461d-857e-1f06f2fff8c4` remains **BLOCKED**, on a narrower
profile/tool-policy decision. No target mutation, resume, provider call, queue
operation or service restart is performed. Both checkouts were inspected and
reviewed host authority was used for exact read-only configuration evidence.
No agent-facing restriction was mistaken for a Support diagnostic prohibition.

## Correction to escalation 1

The retained snapshot is not the sole resume configuration authority. The source
merges the snapshot with stored provider configuration, stored session mode and
explicit user overrides. The effective retained metadata for this exact session
is now independently checked:

- Model `gpt-5.3-codex` in both snapshot and stored runtime configuration.
- Autopilot mode in both stored session mode and stored runtime configuration.
- `allow_all=on`, `reasoning_effort=high` in retained runtime options.
- No explicit model/mode override; no original-effective model record.
- Baseline model `claude-sonnet-5` is comparison metadata, not model-selection
  authority and must not be selected as a fallback.

`initializeACPSession` passes retained model/mode/options after profile defaults;
those retained values take precedence. **Selecting another compatible profile
would not inherently force its default model or mode on this session.** The
prior result's implication that `gpt-6.1-terra`/agent would necessarily replace
`gpt-5.3-codex`/autopilot is withdrawn. No new Human model decision is required
merely because the profile defaults differ. This is configuration/source
qualification, not a native/provider-model availability probe or acceptance.

Seven existing effective-configuration tests pass offline. Seven native CLI
policy test functions, including34 subtests, also pass. Actual source files and
raw output are pinned here. These focused packages do not need the absent SDK
that prevented the earlier broad lifecycle-package tests from starting. That
old cache limitation is not an authority or repair blocker.

## Remaining concrete difference

The removed AutoPilot Review profile has native `--no-ask-user` enabled. The
current Human-maintained Review profile `2653283e-3df4-48d6-8242-774b24b54f38`
has it disabled. Their file/tool/URL allow flags otherwise match.

The Copilot registry calls this flag **Disable ask_user tool**. Native command
construction resolves CLI flags from the selected profile for launch/restart;
they are not restored by the ACP model/mode/options layer above. The current
Review profile would therefore omit an explicitly retained native tool-disable
flag. No supported per-session CLI-policy preservation override was identified
in the inspected request/configuration contracts. The actual provider's unseen
tool catalogue is not queried or assumed; source/configuration does not prove
identical native tool availability without that flag.

This is not a proposed host/filesystem privilege increase and is not a reason
to widen guards. It is a concrete native tool-policy difference under the
request's requirement to preserve existing role/tool scopes. Treating it as
irrelevant or hiding it behind a model-default comparison would make the
successor choice ourselves.

The task remains in Backlogs with no step profile and no workflow default.
The future Review step's current profile does not declare itself a retroactive
successor for this FAILED Backlogs session. The task's registered Worker profile
uses a different provider family and is not a same-identity replacement. Existing
launch/start-created/workflow/recovery paths cannot be called as inert probes:
they may execute, reroute, reset or replace state. No public metadata-only
rebind route was identified. No direct agent SQL/rebind capability is introduced.

## Exact existing owner and finite next action

The existing Human maintainer of this workspace's PR Review workflow/profile
bindings must either:

1. Designate an existing compatible successor for original session
   `16bcc779-85b0-45a1-9ab7-46f3668fe410` that retains the native ask_user-disabled
   policy and existing scopes; or
2. Explicitly authorize current Review profile
   `2653283e-3df4-48d6-8242-774b24b54f38` and its different native interaction/tool
   policy for this exact preserved session under a separately scoped contract.

If neither is intended, retain the hold. Do not reconstruct/undelete the removed
profile, manufacture a successor mapping, choose another provider, or silently
modify the current Review profile for all other sessions. **No new approval of
the discovery/bootstrap objective or a speculative model change is requested.**
The missing input is the successor/native-tool-policy identity, which the
original request explicitly reserves when genuinely unproven.

After that input, a separately reviewed metadata-only same-session operation
must preserve workspace/task/session/primary IDs, FAILED state, Backlogs and
Human promotion, retained runtime model/mode/options, continuation and queue
incarnation/generations/audit. No resume/drain/replay is included. Root's one
instruction acceptance follows only after binding repair and readback, using
an explicit fresh submission or authenticated retained entry as separately
bound; the previous failed window supplies no replay permission.

## Exact status

No product source change, candidate build, installation, binary overlay,
caller/profile/tool grant, guard change or lifecycle activation occurred.
The session still names removed profile `62434899-7d6b-432d-b93f-0b71c67eb35d`;
the exact metadata read reports its original FAILED update timestamp unchanged.
All prior evidence remains retained. The prior stored binary hashes remain
historical installation evidence; no source tag is represented as a new binary
build attestation. The existing broker returns this result proactively, without
task messaging, polling or a Human relay by Support.
