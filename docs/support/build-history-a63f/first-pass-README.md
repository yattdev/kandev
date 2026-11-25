> Historical first-pass report; superseded by escalation pass 1.

# Successful build ownership across runtime model replacement

Request `a63f09f3-5213-4a87-add9-a983718059eb`.
Status: **source repair and private candidate qualification complete; current
owner recovery BLOCKED by legacy proof and preserving exposure requirements**.
No installed code, owner resource, identity, model or claim is changed.

## Exact installed semantics and incident diagnosis

The installed broker stores one model per `(task_root, project)` at
`model_path`, plus an immutable task/clone/project claim. `save_scoped_model`
atomically replaces the entire current model; it does not keep a build-history
record. `render_model_for_command` runs for `config`, `build`, `run` and other
model commands. Thus a runtime overlay with an image but no build replaces the
prior build model for that alias, even though the immutable project claim
continues to belong to the same clone.

`owned_build_image` and `Operations.build_images_here` authorize images by
scanning those current models and requiring a scoped service image **and a
build definition**. The runtime model's reference alone does not qualify.
Exact-tag lookup consequently fails before an image engine query; typed-ID
lookup also loses its candidates. A previous successful engine build and
metadata acceptance do not persist an independent ownership receipt.

The source and installed private characterization reproduce precisely:

1. Start with no saved model or history and perform an authenticated selected
   service build against a private fake engine. Tag/ID admission initially works.
2. Save a same-project image-only runtime model with `read_only`, non-root user
   and `network_mode: none`. The synthetic replacement uses `config` so no
   runtime container is required; the actual `run` path shares the same model
   rendering/saving function.
3. The original scoped tag is denied with `IMAGE_NOT_AUTHORIZED` without
   querying it. The immutable task/clone/project claim is unchanged.

This is a lifetime defect, consistent with the supplied build365/runtime366
sequence, not a recurrence of the 6b30 type-discrimination bug or the 5746
bootstrap question. Support has not read the owner's private model, claim,
image, source, receipts or environment and does not claim independent live
attribution beyond the supplied sequence and source-identical characterization.

There is no installed history reader, success-receipt import or restoration
verb. `config` with an old build definition would create current-model admission
from a model alone; it is **not** an acceptable recovery here. Listing images,
using another type/actor or inspecting the raw ID does not establish historical
ownership. No such workaround is proposed or attempted.

## Minimal source repair

The candidate adds durable successful-output receipts separately from the
mutable current model. A receipt is written only after an admitted engine build
returns zero and a bounded `image inspect --format '{{json .Id}}' <scoped-tag>`
captures the actual local output ID. Model registration, a runtime reference,
failed build, dry-run, print/check mode or caller-supplied metadata cannot create
one. No public receipt-writing/import command is added.

Receipts bind task root, exact clone directory, existing project claim, service,
scoped output tag, engine `.Id`, version and observation timestamp. They are
platform-owned single-link regular files, mode 0600, bounded to 8 KiB each and
64 current tag receipts per exact clone. Writes use a private file, fsync,
atomic replacement and directory fsync. Reads check stable file custody,
record/path binding and the still-valid immutable project claim. They do not
load source, environment, package data, application config or customer content.

Historical metadata admission requires the current tag to resolve to the saved
engine ID. Retargeting the tag cannot admit another image. Missing/foreign,
sibling-clone, revoked-claim, malformed, symlinked, writable and over-limit
receipts fail closed. Retained tag reads use the existing curated typed-ID
projection; private image environment and entrypoint are not exposed.
Registry manifest/RepoDigest and local engine `.Id` remain separate identities.

The existing active-build-model predicate remains compatible. It does not
become successful-build provenance; the new retained admission path requires
the independently captured success receipt. Current runtime models are never
silently replaced by historical build definitions. Scout, volume, container,
network, task-access and lifecycle grants are not expanded.

For Compose, receipt capture covers the precise selected-service build form:

```text
build [--pull[=true|false]] [--no-cache[=true|false]] <one-build-service>
```

Global dry-run is excluded. Other modes remain supported as before but produce
`BUILD_HISTORY_NOT_RECORDED` rather than falsely attesting which outputs were
built. Standalone admitted local `docker build` also records its actual scoped
output after success. If identity capture or persistence fails after an engine
zero exit, return 78 with `BUILD_COMPLETED_RECEIPT_UNAVAILABLE`; the build may
already exist and **must not be retried automatically**. No cleanup or rollback
of images/resources is attempted.

## Source, installed and loaded status

Deployment baseline: `4b23f583ce34876d50b3e3fb1aa6e065ee749d5c` (full revision in the review manifest).
Both checkouts were inspected. Product primary source HEAD remains
`cfd6bdf65ae6f631d7701c105791a099c0325cc3`, with all unrelated changes preserved.
This is a deployment policy repair; no product Go, binary overlay, Dockerfile,
Compose deployment, entrypoint, service or installer changes are required here.

Installed broker SHA256 remains
`9514b692d9833fcc64c1978b7553c181c37e84625e645a0ad0521d4b2f298e91`;
installed operations SHA256 remains
`d176a1794af1f5138a9b77d3594f09b9063277f6ea2183877891bb49c7abe7e4`.
Initial and final public-file readbacks match exactly, including wrapper/client/
guard custody. The candidate is loaded only inside private fixture namespaces.
No candidate image or binary build, installation, service restart or lifecycle
candidate activation occurs. The existing installed lifecycle surface is
preserved and is not invoked on behalf of this request.

`candidate-review.json` binds the exact candidate pair and tests. Source AST
comparison verifies all preexisting broker functions/classes except the build
completion hook are unchanged. In the operations module only four existing
methods change; lifecycle, volume, native worker and all other top-level
handlers remain identical to the deployed preserving baseline.

Installing the operations module alone is **not supported**: it depends on
new broker receipt functions. Compose build handling is long-running loaded
broker code, so hot-copying the module cannot activate the repair. A preserving
reviewed broker/module exposure is required; this request prohibits restart
and provides no authority to perform it. Any later Kandev image deployment
must use `scripts/kandev-safe-deploy` and its normal readiness/rollback gates,
never bare Compose recreate. No image deployment is attempted here.

## Independent qualification

- Unchanged installed pair: one synthetic characterization passes, demonstrating
  build admission followed by denial after runtime-model replacement.
- Final candidate: 26 cases pass with actual installed wrapper/client, HMAC,
  Unix transport and Compose 5.7.1 parser. The full build -> runtime model ->
  exact tag/typed ID sequence runs through that real transport. Engine responses
  and all models/claims/receipts are private synthetic fixtures.
- Source mode: 24 pass, two installed-transport cases skipped; normalized
  parser fixture only. Actual parser coverage is the candidate fixture above.
- Existing regressions pass: image 18; lifecycle 22; volume 24; broker 16+11
  and real Unix transport; task access passes with its new bounded post-build
  identity response and receipt assertion. No assertion or guard is weakened.

The candidate fixture runs under an empty filesystem root and private network
namespace. Live `/data`, `/run`, `/app`, credentials and engine sockets are
absent. Reviewed public broker/module source is staged as platform-owned files;
actual installed wrapper/client and parser are retained. No live task IDs or
resources are used; no actual engine build, registry/package/provider contact,
app/test runtime or board operation occurs. See the test sources and raw output.

## Existing build365: reserved recovery boundary

The source repair protects future successful outputs. It intentionally does
not invent a receipt for `kd_8355_connector346_php-test:dev365-b07-c054` from its
name, a current model, a project claim, a local image's mere existence, or a
reported digest. The already-completed build predates this receipt mechanism.
The source patch alone therefore does **not** make Root's current acceptance
succeed after installation. Re-registering a historical build model is not
part of the fix or the next owner command.

A concrete [legacy recovery design](legacy-recovery-design.md) records the
smallest reserved input and operation: trusted historical successful-build
attestation plus a separately reviewed exact-scope migration and preserving
exposure. It exposes no generic agent import capability and admits no sibling
or unregistered image. Support has not read or acted on the owner receipt
mentioned in the request. Without authentic proof, legacy admission stays denied.

Finite next action: route that design and the tested pair to the broker
maintainer for reviewed preserving exposure **and** a trusted build365 recovery
contract. Root supplies/binds the original authoritative build and typed identity
proof through the approved support boundary. Only after those prerequisites
may Root bind one same-owner metadata acceptance. No owner inspect/config/build/
run retry, new actor, tag fabrication, resource change or polling is authorized
by this source-only result.
