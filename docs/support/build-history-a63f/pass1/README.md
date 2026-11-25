# Escalation pass 1: decoupled reader and exact native-proof handoff

Request `a63f09f3-5213-4a87-add9-a983718059eb`.
The earlier source repair remains valid, but two parts of the blocker were too
broad: metadata receipt reading need not wait for a broker restart, and absence
of broker history does not prove absence of native BuildKit evidence.

## Restart-free reader correction

Receipt validation and standalone post-build capture now live in the already
hot-loaded operations module. They depend only on APIs present in the unchanged
installed broker (`9514b692...`). The source broker's future Compose completion
hook delegates to that shared module. This removes the API coupling that made
the first candidate unsuitable for module-only exposure.

Three compatibility cases pass against the **old installed broker plus the new
module**, including actual installed wrapper/client transport: a proven private
fixture receipt survives runtime-model replacement; no receipt stays denied;
a changed tag fails its pinned-ID check. No live legacy receipt is imported.
The full candidate still passes 26 cases. Image 18, lifecycle 22, volume 24,
broker 16+11 and transport, and task-access regressions pass. The same lifecycle,
volume/native-worker and unrelated operations code is preserved by source AST
comparison. The module has no public receipt-import or native-history command.

`reviewed.json` pins the metadata module, existing broker/wrapper/client/guard,
source tests and exact installed predecessor. Exposure uses the existing
hash-gated atomic module installer, with a private backup and no restart.
`installation.json` and installed fixture results, when present, record the
actual effect; source qualification alone is not installation or owner acceptance.
The forward automatic **Compose** capture hook remains source-only: this request
still forbids a broker lifecycle operation. Standalone admitted builds can
capture their own successful outputs using the shared hot module.

There is no receipt for Build365 created by this repair. Reading a runtime model,
a task-written receipt, a tag name, an image label or the supplied combined
image-ID/RepoDigest notation cannot create one. No affected-owner metadata,
config, build or run is performed. Root must keep the owner stopped.

## Native history audit

Socket-free help in an empty-root/network namespace identifies the public
installed plugin as Buildx v0.38.1, revision
`0dbe87f36ed472fe36b6796c3f70b4210a731fc5`. Public binary/script hashes and raw
help are retained. This identifies reported client version/source; it does not
attest the live BuildKit server version, record availability or an owner build.
No engine-history request is made.

The installed restricted wrapper rejects `buildx history` before an engine
command. Three new baseline characterization cases pass using private models
and the real wrapper/client/parser, confirming both the original model-loss
behavior and that native history is not an already-admitted owner route.

The version-bound primary source establishes these limits:

- [Buildx listing implementation](https://github.com/docker/buildx/blob/v0.38.1/commands/history/ls.go)
  returns reference/status/times, not exporter output identity. Its local option
  uses repository identity, which is not an exact task/clone ownership proof.
- [Query/filter implementation](https://github.com/docker/buildx/blob/v0.38.1/commands/history/utils.go)
  supports reference/repository/status/time filters, not exact output-tag
  filtering. A specific native record reference can avoid a shared-engine census.
- [Inspection output](https://github.com/docker/buildx/blob/v0.38.1/commands/history/inspect.go)
  does not expose raw `ExporterResponse`. Full output can include build arguments,
  source/error/provenance information. Guessing an output template is not a
  qualified proof reader.
- [Export implementation](https://github.com/docker/buildx/blob/v0.38.1/commands/history/export.go)
  bundles native records with other content/state; `--finalize` can mutate history.
  Broad export, logs, import, prune or finalization are not acceptable substitutes.
- The client's [dependency declaration](https://github.com/docker/buildx/blob/v0.38.1/go.mod)
  names BuildKit v0.34.0. That version's
  [native history schema](https://github.com/moby/buildkit/blob/v0.34.0/api/services/control/control.proto)
  includes completion/error, exporters and exporter responses. It is a possible
  engine-owned proof source, not evidence that this server retained Build365.

## Revised minimum next input

The [exact-record handoff](native-proof-handoff.md) narrows the next operation.
Root supplies the original protected Build365 execution evidence, including its
native BuildKit record Ref and builder/node identity if available, exact output
tag and separately observed typed engine `.Id`. These must come from already
produced evidence; no owner retry is needed. A short Kandev native-receipt ID,
image ID and registry digest are not interchangeable with a BuildKit record Ref.

A separately bound Support metadata review can then evaluate that one protected
record/evidence tuple using existing reviewed host authority. The first pass
should not have assumed a new trust issuer or human approval is always needed.
It remains essential to establish actual successful output provenance before
writing a single exact-scope retained receipt. No generic agent import grant,
model re-registration or global history discovery is authorized.

No safe current owner command can manufacture the missing proof. The remaining
blocker is the unbound authentic historical evidence/restoration step, not the
metadata reader's restart requirement. Root's one same-owner acceptance remains
separate and pending; it must not run merely because the reader is exposed.

Both checkouts were rechecked. No upstream-only limitation is claimed. Available
tools provide reviewed host execution but no separate callable broker-maintainer
or task-receipt reader. Host escalation was used for private/public-source
qualification, with no review rejection. No service restart, new trust grant,
provider/target/customer operation, claim/data mutation or live owner proxy is
performed. This result returns through the existing Support broker notification
path; no human message relay or task polling is requested.

## Installed result

The decoupled reader is installed from deployment source commit `cb2ba9121d75190bb140538ac6149787731a848e`.
Operations SHA256: `d60ccdfeb55ea4cca64e10d1e68639f7f1cafa0d6705b8cb78be6a20bc6d59ee`.
The broker stays `9514b692d9833fcc64c1978b7553c181c37e84625e645a0ad0521d4b2f298e91`;
wrapper/client/guard hashes match the review. Container identity, image and start
time match before/after in `installation.json`; no restart or binary/image
replacement occurred. The existing module backup is retained privately.

All three reader compatibility cases pass against the **actual installed pair**,
including real wrapper/client and protected synthetic success receipts. All
three installed boundary cases also pass: no receipt does not recover a tag,
and native `buildx history` remains outside the owner catalog. The installed
image suite passes 18 cases and lifecycle suite 22. All fixtures hide live task
state and use a fake engine; these are not affected-owner acceptance.

The forward Compose completion hook is still not installed. No Build365 receipt
is seeded, no project claim/model is restored, and no owner image is inspected.
Actual recovery remains unverified and blocked on the separately bound authentic
historical-proof/restoration operation. Root must not retry on installation alone.
