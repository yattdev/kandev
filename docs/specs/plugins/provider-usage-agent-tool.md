---
status: approved
created: 2026-08-19
owner: Kandev
---

# Provider Usage Agent Tool

## Why

The long-lived Office board coordinator and ordinary task agents need a machine-routable, read-only
view of live provider capacity so they can choose a provider or session route while leaving workflow
columns and task state authoritative. Agent-profile enumeration proves a profile is configured; it
never proves a subscription is signed in, available, fresh, or below quota. Kandev's MCP surface
currently exposes no such signal, and scraping the [Provider Usage plugin](https://github.com/kdlbs/kandev-plugin-provider-usage)'s
UI would duplicate authorization, lose task/session binding, and make presentation text an unstable
routing contract.

This spec is the durable contract for one plugin-contributed agent tool that closes that gap. It
builds on the generic mechanism in [Plugin-Contributed Agent Tools](agent-tools.md); read that spec
first for how tool declaration, discovery, invocation, and revalidation work in general. This spec
covers only what is specific to this one tool: its schema, state taxonomy, freshness rule, and scope.

## Repository ownership

The tool is implemented entirely in the separate repository
[`kdlbs/kandev-plugin-provider-usage`](https://github.com/kdlbs/kandev-plugin-provider-usage), listed
in this repository's `plugin-registry/plugins.yaml`. It is not a submodule and its production code
does not live in `kdlbs/kandev`. That plugin already owns the codexbar/Augment adapters, the
background poller, provider fan-out, and the in-memory snapshot its own UI reads; the tool is a new
read projection over that existing snapshot, not a second data source. `kdlbs/kandev` contributes only
this durable contract, public documentation, and a packaged-plugin end-to-end conformance test.

## What

- The plugin declares one `agent_tools` entry with plugin-local name `get_provider_usage`, targeting
  both `kanban-task` and `office-task` surfaces, with input schema
  `{"type": "object", "properties": {}, "additionalProperties": false}` — no properties, and
  `additionalProperties: false` so a caller cannot pass an extra property and have it silently
  ignored. A bare `{}` schema would accept any properties under standard JSON Schema semantics, which
  would not actually enforce "no arguments accepted"; the host's generic schema validation (per
  [agent-tools.md](agent-tools.md)) rejects a call with any property before the plugin is invoked only
  because `additionalProperties` is explicitly `false`. Per the generic naming rule in [agent-tools.md](agent-tools.md),
  the plugin ID `kandev-provider-usage` slugs to `kandev_provider_usage`, so the host-derived MCP tool
  name is `kandev_kandev_provider_usage_get_provider_usage`. A bare global `get_provider_usage` name is
  not authorable under that contract.
- The tool accepts no `refresh`, account selector, task ID, workspace ID, credential, or polling
  control argument of any kind. It reads the plugin's existing in-memory snapshot only. Only the
  plugin's existing background poller and an explicit human UI **Refresh** click may contact a
  provider. This is what makes concurrent and repeated coordinator calls free: they can never amplify
  provider traffic.
- The manifest declares `read_only_hint: true`, `destructive_hint: false`, `idempotent_hint: true`,
  `open_world_hint: true`.
- On plugin startup before the first background poll completes, the tool returns an immediate
  well-formed partial/unknown response instead of synchronously running codexbar inside the call. The
  host's MCP tool deadline (30 seconds) is shorter than the plugin's full multi-provider report, so the
  tool path MUST NOT perform any provider I/O under any condition.

### Response schema (`schema_version: "1"`)

Top level:

| Field | Type | Notes |
|---|---|---|
| `schema_version` | string | Always `"1"` for this contract. |
| `evaluated_at` | string (RFC3339, UTC) | One instant captured per tool call; every derived age/staleness value in the response is computed from this same instant. |
| `snapshot_generated_at` | string (RFC3339, UTC) or `null` | When the underlying poller snapshot was assembled; `null` before the first poll completes. |
| `poll_interval_seconds` | integer | The plugin's configured poll interval (minimum 60). |
| `stale_after_seconds` | integer | Always `2 * poll_interval_seconds`. |
| `partial` | boolean | `true` when at least one provider's data in this response came from a refresh failure rather than a successful poll (see Failure modes). |
| `scope` | object | See below. |
| `providers` | array of provider records | May be empty only before the first poll completes. |

`scope`:

| Field | Type | Notes |
|---|---|---|
| `usage_scope` | string | Always `"instance"` in this version. |
| `user_scoped` | boolean | Always `false` in this version. |
| `invocation_workspace_id` | string | The workspace ID Kandev bound to this invocation. Present for audit context only — it does not partition the returned data; every authorized caller sees the same install-wide snapshot. |

Provider record (one per provider, and potentially per account once multi-account adapters exist):

| Field | Type | Notes |
|---|---|---|
| `provider_id` | string | Open string, not a closed enum, so a future provider needs no schema change. Stable across calls for the same provider. |
| `provider_name` | string | Stable human-readable display name. |
| `account` | object or `null` | Safe, non-sensitive account metadata only (see Permissions). `null` when the adapter documents no stable non-sensitive identifier. |
| `support_state` | string enum | `supported \| unsupported \| unknown`. |
| `availability_state` | string enum | `available \| quota_exhausted \| provider_unavailable \| telemetry_stale \| not_configured \| unsupported \| unknown`. |
| `fetched_at` | string (RFC3339, UTC) | When this provider's retained data was last successfully fetched. |
| `age_seconds` | integer ≥ 0 | `evaluated_at - fetched_at` in whole seconds. |
| `stale` | boolean | `age_seconds >= stale_after_seconds`. |
| `windows` | array of window records | May be empty for `not_configured`/`unsupported`/`unknown`/first-poll-partial records. |
| `reason` | object or absent | Present when `availability_state` is not `available`. See Reason shape below. |
| `warnings` | array of strings or absent | Sanitized, non-secret warning codes/messages (e.g. "refresh failed, showing last known value"). |

Window record:

| Field | Type | Notes |
|---|---|---|
| `window_id` | string | Stable per provider (e.g. a rate-limit slot identifier), machine-comparable across calls. |
| `window_name` | string | Human-readable window label. |
| `window_seconds` | integer or `null` | Window duration when known. |
| `scoped` | boolean | `true` when the window applies to a specific model/target rather than the whole provider. |
| `utilization_percentage` | number | 0-100. |
| `remaining_percentage` | number or absent | `100 - utilization_percentage` when derivable from the source data. |
| `reset_at` | string (RFC3339, UTC) or `null` | When known. |
| `availability_state` | string enum | Same enum as the provider-level field, scoped to this window. |

Reason shape (used at both provider and window level):

| Field | Type | Notes |
|---|---|---|
| `code` | string | Stable machine code from a small allowlist (e.g. `quota_exhausted`, `fetch_failed`, `missing_credentials`, `platform_unsupported`, `classification_unknown`). |
| `retryable` | boolean | Whether a later call is expected to observe a different state without operator action. |
| `user_action_required` | boolean | Whether a human must act (e.g. sign in) before the state can change. |
| `reset_at` | string (RFC3339, UTC) or absent | Present when the reason is `quota_exhausted` and a reset time is known. |

Never present anywhere in the response, fallback text, or plugin logs: raw provider stderr, raw auth
responses, file paths, HTTP headers, tokens, emails, raw upstream error bodies, or any other credential
or account secret.

### State derivation rules

- A non-scoped window at or above 100% utilization makes that window `quota_exhausted`, with
  `remaining_percentage: 0`. It also makes the owning provider `quota_exhausted`; the provider's
  `reason.reset_at` is the earliest known future reset among its exhausted non-scoped windows.
- A **scoped** window (`scoped: true`) can be `quota_exhausted` without making the whole provider
  exhausted — a parameterless tool has no way to know which model the coordinator intends to target, so
  the exhaustion stays explicit on that window plus a provider-level warning. Callers routing a
  specific model MUST inspect window-level state, not only the provider aggregate.
- `not_configured`: a recognized missing-login/missing-local-credential condition reported by the
  provider adapter or codexbar itself.
- `unsupported`: an explicit "this provider/platform is not supported" signal from the adapter.
- `provider_unavailable`: a transient fetch/CLI/provider failure with no usable cached success to fall
  back on.
- `telemetry_stale`: a retained success whose `age_seconds >= stale_after_seconds`. Windows remain
  present for context, but routing consumers must treat this differently from `quota_exhausted` and
  `provider_unavailable` — the last known numbers may no longer reflect reality.
- `unknown`: any evidence that does not match one of the above. Classification uses a small, explicit
  allowlist of stable adapter/codexbar kinds and codes; it never infers a confident state by parsing
  arbitrary upstream English. Configured agent profiles are never consulted for any of these states — a
  profile existing proves nothing about live availability.
- A valid `quota_exhausted` response is complete telemetry, not a partial failure; it does not set
  top-level `partial`.

## Permissions

- Discovery and invocation follow the generic plugin-agent-tool authorization contract in
  [agent-tools.md](agent-tools.md): only an active plugin's declared tool on a matching backend-bound
  task surface is discoverable, and Kandev injects the bound task/session/workspace/surface context
  that the caller cannot override through arguments.
- Data is **instance-wide, not user- or workspace-scoped**. `invocation_workspace_id` in the response
  identifies which workspace's session made the call for audit purposes; it does not mean the returned
  provider data is private to that workspace. Any authorized task session on the installed instance
  sees the same snapshot. Operators who need workspace-private quota data need a different, not-yet-built
  credential/ownership model — see Out of scope.
- `account` metadata, when present, is limited to safe, non-sensitive fields the adapter documents as
  stable and non-sensitive (for example a short opaque label). It is never a raw provider account
  identifier, an email, or an email hash.

## Failure modes

- A failed provider refresh does not evict a sibling provider's result, and does not evict its own
  prior success. The plugin retains, per provider, its last success plus its latest normalized
  failure.
- If a refresh fails while the retained value for that provider is still within `stale_after_seconds`,
  the provider's computed state remains whatever it was (`available`, `quota_exhausted`, etc.), a
  `warnings` entry reports the failed refresh, and the top-level `partial` is `true`.
- Once a retained value crosses `stale_after_seconds` without a successful refresh, its state becomes
  `telemetry_stale` regardless of what it was before.
- A provider with no cached success at all and a failed current fetch is `provider_unavailable`. This
  never fails the tool call as a whole — a Claude fetch failure does not hide a healthy Codex record,
  and vice versa.
- Disabled, errored, or uninstalled plugins fail closed per the generic contract: the tool is not
  discoverable and a call fails without entering the plugin.
- On first startup before any poll has completed, the tool returns a response with `providers: []` (or
  provider entries whose `availability_state` is `unknown`), `snapshot_generated_at: null`, and
  `partial: true` rather than blocking on a synchronous fetch.

## Persistence guarantees

- The underlying poller snapshot (per-provider last success and last failure) lives in the plugin
  process's memory. It does not survive a plugin process restart; a fresh poll populates it again on
  the plugin's normal interval, and the tool serves partial/unknown responses in the interim exactly as
  it does on first startup.
- No new database entity, cache, or credential store is introduced by this tool. It reads the same
  cache the plugin's existing UI webhook reads; it does not add a second cache or a second provider
  client.
- Tool declaration and plugin lifecycle status persist per the generic plugin contract in
  [agent-tools.md](agent-tools.md) — unaffected by this spec.

## Scenarios

- **GIVEN** Provider Usage is installed and active, **WHEN** an Office coordinator or Kanban task agent
  lists MCP tools on its bound surface, **THEN** `tools/list` includes
  `kandev_kandev_provider_usage_get_provider_usage` with an empty input schema, the schema above as
  output schema, and `read_only_hint: true, destructive_hint: false, idempotent_hint: true,
  open_world_hint: true`.
- **GIVEN** the same installed plugin, **WHEN** a Configuration or External MCP client lists tools,
  **THEN** that tool is absent.
- **GIVEN** healthy retained Claude and Codex telemetry, **WHEN** the tool is called, **THEN** the
  response contains both provider records with `support_state: supported`,
  `availability_state: available`, utilization/remaining percentages, window duration/name, reset time
  when known, `fetched_at`, deterministic `age_seconds`/`stale`, `partial: false`, and no credential
  material anywhere in the payload.
- **GIVEN** a non-scoped provider window at 100% utilization with a known reset time, **WHEN** the tool
  is called, **THEN** that window and its owning provider are both `quota_exhausted`, the window's
  `remaining_percentage` is `0`, and the provider's `reason` reports `code: "quota_exhausted"` and that
  `reset_at`.
- **GIVEN** Claude's most recent refresh failed and it has no retained success while Codex succeeded,
  **WHEN** the tool is called, **THEN** the call succeeds as a whole with Codex `available` and Claude
  `provider_unavailable`, and top-level `partial` is `true`.
- **GIVEN** a provider's retained success is exactly `2 * poll_interval_seconds` old with no successful
  refresh since, **WHEN** the tool is called, **THEN** that provider's `stale` is `true`, its
  `availability_state` is `telemetry_stale`, and its `windows` remain present with their last known
  values.
- **GIVEN** a codexbar-reported missing-login condition for a provider, **WHEN** the tool is called,
  **THEN** that provider is `not_configured`; an explicitly unsupported provider is `unsupported`; an
  unrecognized failure is `unknown`; none of these three are affected by whether an agent profile for
  that provider exists.
- **GIVEN** repeated or concurrent calls within one poll interval, **WHEN** the tool is invoked
  multiple times, **THEN** every call returns the same immutable snapshot and the plugin's fake/real
  provider client observes no additional invocation.
- **GIVEN** a caller supplies a foreign task ID, session ID, workspace ID, or account selector as a
  tool argument (the schema takes none, so this means a malformed/extra-property call), **WHEN** the
  tool is invoked, **THEN** Kandev's generic schema validation rejects the call before the plugin is
  entered, and the response never reflects a value other than the backend-bound context.
- **GIVEN** the plugin is disabled or uninstalled, **WHEN** an agent that previously discovered the
  tool invokes it, **THEN** the call fails closed without entering the plugin, per the generic
  contract.
- **GIVEN** injected token/header/email/path-shaped strings in a simulated upstream failure or local
  config, **WHEN** the tool is called and the call is logged, **THEN** none of those strings appear in
  the tool's fallback text, structured content, or the host's audit log entry for that invocation.
- **GIVEN** the manifest's output schema, **WHEN** it is validated against a response containing an
  unrecognized future `provider_id`, **THEN** validation still passes; **WHEN** it is validated against
  a response missing a required routing field or containing an invalid `availability_state`/
  `support_state` value, **THEN** validation fails.

## Out of scope

- A built-in host `get_provider_usage` tool backed by `internal/agent/usage`, an MCP resource, a second
  MCP server, or scraping the plugin's rendered UI or webhooks.
- New provider API clients, a duplicate cache, or any direct provider call from the MCP invocation path
  — the tool only ever reads the plugin's existing poller snapshot.
- Automatic task moves, workflow-column changes, session spawning, provider fallback, or routing-policy
  mutation. The coordinator consumes this as a read-only signal and keeps board state authoritative;
  this tool has no side effects.
- Inferring availability from configured agent profiles, saved models, or execution-profile existence.
- Per-user or per-workspace plugin installation, enablement, credentials, or telemetry partitioning —
  the current contract is deliberately instance-wide (see Permissions).
- Raw auth material, raw account identifiers, email hashes, raw upstream error bodies, billing ledgers,
  token costs, or absolute remaining units the source does not provide.
- Changing the existing Provider Usage UI presentation or removing its manual Refresh control; the UI
  and this tool read the same shared snapshot and neither becomes the other's source of truth.
- Adding new providers beyond the plugin's existing Claude/Codex (and any already-shipped) adapters;
  future providers only rely on the open-string `provider_id`/schema extensibility already defined
  above.
- A refresh argument or any other input parameter on this tool. A future rate-limited, explicitly
  cooldown-guarded refresh mode would need its own spec.
