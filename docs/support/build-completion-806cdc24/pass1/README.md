# Zero-restart selected Compose completion capture

Request `806cdc24-d005-4605-b6ca-fd7ee61f21fe`, escalation pass 1.
This source candidate adds a narrow adapter in the existing per-request module.
Installation and installed qualification are recorded separately in
`installation.json` and the installed test logs when those files exist.

**Installed result:** the hash-gated installation passed automatic approval
review and replaced only the hot module. Source commit
`c4f5b4e` installs operations SHA256
`1f075423e4c89e32caa359e2b27e12086aad4d0adc1f5e4eafe54052a1e7608a`.
All 87 installed private cases pass without skips. Broker, wrapper, client,
guard, container/image identity, start time and restart count are unchanged.
No rollback was needed. See [result.json](result.json),
[installation.json](installation.json) and
[installed-public-hashes.json](installed-public-hashes.json).
Actual owner acceptance has not run; Root owns the separately bound procedure
below. No historical receipt or real task/model/resource was inspected or
modified by Support.

The first pass correctly identified two defects in the previous candidate:
the unchanged `compose` branch does not call the hot module, and post-build tag
inspection alone is not output provenance. Its activation conclusion was too
broad: a selected-build adapter can use the existing authenticated module route
without replacing or restarting the broker. Bare `docker compose build` remains
unchanged and does not automatically retain an output receipt.
The older uninstalled broker forward-hook candidate remains unqualified and is
excluded from this exposure; its archived failing test is not a test of the new
adapter. Do not activate that broker candidate as part of this fix.

## Supported command

```text
docker kandev compose-build <fresh-operation-UUID> compose \
  --project-name <unchanged-own-alias> --project-directory <exact-cwd> \
  -f <own-base-model> -f <own-build-overlay> \
  build --pull=false <one-existing-service>
```

Use the original owner, caller environment, root, physical cwd, project alias
and existing task/session. No token, context, profile, wrapper or actor changes
are involved. The explicit project flag is required: the installed broker's
generic module ABI does not forward ambient Compose environment variables.
Models must therefore be self-contained, or use an explicit task-owned env
file through the existing guarded parser. This adapter does not silently promise
to preserve ambient `DB_PORT`/`WEB_PORT` overrides. The affected model has no
published ports; Root must bind and verify the actual inert model separately.

The UUID identifies one operation, not a credential. A fresh output tag is
required. An existing successful-output receipt is never overwritten by this
adapter, including the restored Build365 receipt. A completed identical UUID
can return its prior receipt without rebuilding or replacing the runtime model;
pending, failed, unknown, changed-input or superseded operations never replay.
Root's acceptance still permits only one submission, not automatic retries.

## Preserved authority and execution

The actual installed wrapper/client framing and broker HMAC scope check run
before module dispatch. The adapter calls the existing Compose argument parser,
project options/directory selection, scoped project naming, guarded configuration
parser, model validator, isolated-recreate check and immutable project claim.
It accepts one local build object inside the unchanged cwd/project directory,
with pull/no-cache flags only. Wrong tasks/clones and foreign claims fail closed.

The already-installed, hash-pinned Compose plugin compiles that validated model
with its documented `build --print` interface. Only one target, its exact scoped
tag, matching context/Dockerfile, and `type=docker` output are accepted. Additional
targets, inherited configuration, outputs, secrets, SSH, entitlements and unknown
build keys fail. Template markers are rejected because Bake JSON uses HCL JSON;
a Compose literal must not become a host-side expression.

The broker then invokes the existing hash-pinned native Buildx plugin with a
fixed Bake command, private generated file, private metadata destination and
Docker load output. It does not grant callers arbitrary Bake, metadata-file,
builder, context or exporter options. Those existing command denials remain.
The executor's cwd is only the existing host translation of that same project,
matching the normal Compose path mapping. No alternative engine or credentials
are selected, and no new binary is installed.

[Buildx v0.38.1's Bake source](https://github.com/docker/buildx/blob/v0.38.1/commands/bake.go#L367)
writes per-target exporter metadata after build completion; the adapter also
requires native exit zero. [Docker's metadata contract](https://docs.docker.com/reference/cli/docker/buildx/bake/#metadata-file)
defines the reference, output/config digests and descriptor. Native metadata is
read from a fresh broker-private file with custody, size and stability checks.
The selected target, descriptor, optional exporter image name and native
reference must match the supported format. Failed, timed-out, cancelled, malformed
or nonterminal-shaped results cannot create successful ownership.

The adapter independently resolves the local engine `.Id` twice. It must match
the native output descriptor or the native config descriptor, and remain stable.
The receipt records which role matched, separately recording index, manifest
and config roles. An unproven platform manifest remains null; no registry digest,
config digest and engine `.Id` equivalence is assumed.

## CAS, audit and resource preservation

Per-clone operation serialization and a shared tag lock cover all current
module receipt writers. The existing standalone writer's body is unchanged
behind that lock. New capture receipts use atomic create-if-absent publication;
they cannot clobber an existing record even if another writer ignores the lock.
Ledger updates compare prior bytes under the operation lock. Both records are
durably flushed. Capture preflight, receipt hash readiness, successful commit,
failed/unknown outcome and cached receipt reads are audited without build args,
source contents, environment or credentials. Attempt and output counts are
bounded. Busy or failed operations do not wait, poll, retry or clean resources.

Any nonzero or lost response requires STOP and preservation. Native completion
may already have happened, and a receipt may already have committed before an
audit/ledger error. Never rebuild or change the tag to hide that uncertainty.

The build uses the existing build timeout. The one-off runtime cap and every
broker, wrapper, client, guard, lifecycle, volume, curated-image and selected-worker
boundary remain unchanged. Ordinary bare Compose has no new capture hook.
Only the module is eligible for this exposure; no service/broker restart,
recreate, image deployment, new binary or historical restoration is involved.

## Qualification and installation gate

The [review manifest](reviewed.json) pins the previous module, complete candidate,
unchanged installed dependency pair and native plugin hashes. AST comparison
preserves all prior top-level bindings, lifecycle/volume/worker/reader bodies,
all existing Operations methods except dispatch/help, and the old standalone
writer body. This preserves the documented `4686781`, `f7f7405`, `b8d4e3d`,
`681f606`, `cb2ba91` and earlier volume/worker fixes as a superset.

Candidate tests use real installed Compose/Buildx parsers, wrapper/client and
the unchanged installed broker inside empty-root/no-network namespaces. Native
build execution and image queries are mocked. Twenty strict adapter cases pass,
including retargeting, digest roles, failed/cancelled/timeout results, unknown
metadata, hostile translated configuration, scope/token checks, CAS races,
audit failures, fresh-tag preservation and repeat safety. Existing history 26,
reader compatibility 3, image 18, lifecycle 22 and volume 24 cases also pass.
These are synthetic source/transport proofs, not actual owner acceptance or a
real successful build. No affected task state, source, image, registration,
claim, receipt, database or runtime resource was inspected by Support.

The source is committed on deployment `main`. Installation must use the existing
hash-gated `scripts/install-task-image-inspect.py --review
docs/build-completion-806cdc24/pass1/reviewed.json` under automatic approval
review. Installed fixture results and public file/container before/after hashes
must pass before reporting exposure. Rollback restores only the backed-up prior
module through the same hash/CAS gate; it never changes receipt/model/resource
data. A rolled-back candidate is a failed installation, not RESOLVED.

## One separately bound original-owner acceptance

Root first binds the updated inert COPY/chown recipe, current complete frozen
source/WIP inventory, lock/vendor conditions, selected service, unchanged alias,
fresh derived output tag, local previously qualified base, model/overlay hashes
and one operation UUID. The stale 1092-file context is not current-source proof.
Support has neither read nor updated any of those private inputs. Public-source
tests do not establish offline engine resolution; Root must retain its existing
base/provisioning restrictions and must not infer a zero-registry guarantee.

Once installation is verified, the same original owner submits the exact command
above once from its existing root, with explicit `--pull=false` and recipe
`pull: false` / build network restrictions bound by Root. Root supplies literal
values; do not infer a service name from the image tag. Require exit zero and
the final JSON stdout line to match the UUID, fresh tag, engine ID, receipt hash
and `existing: false`. Preserve raw stdout/stderr and hashes.

Within that same finite acceptance, Root may separately bind one admitted inert
runtime-overlay `config --format json` and one curated exact-tag / typed-ID
metadata check. No DB/app/test container, import, network or volume operation is
part of this acceptance. The returned `.Id` must match the captured engine ID;
retention after model replacement must work without model-only bootstrap.
On a refusal, nonzero result or uncertainty, STOP without fallback/retry.
