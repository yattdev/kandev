# a63f escalation 2: native historical proof and one-output recovery

Request `a63f09f3-5213-4a87-add9-a983718059eb`.

The earlier historical-proof blocker was not exhausted: reviewed host Support
can read native build-history metadata without granting engine access to an
agent. Both deployment and platform source checkouts were inspected. This is a
deployment ownership-history issue; no platform product source edit is needed.

## Evidence recovered

A bounded read of at most 256 recent Buildx client reference metadata files found
one matching exact task-root component and service target. The initial root-UID
filter examined zero files; the corrected check uses the actual metadata owner,
UID1000. Client state is discovery only, never a successful-build witness. No
foreign paths/records or source contents are disclosed.

Exact builder/node: `default/default`; native Ref `imhco5xoke5vkderq2l42eltr`.
A read-only native `ListenBuildHistory` call with that Ref, EarlyExit and Limit1
returned completion without error. Its declared image exporter names exactly
`docker.io/library/kd_8355_connector346_php-test:dev365-b07-c054`. Native creation
`2026-11-25T21:19:55.40564477Z` and completion
`2026-11-25T21:22:26.552291255Z` match the supplied Build365 interval. This is
independent engine-retained success evidence, not the mutable runtime model.

The record has no exporter-response digest. Its result points to an OCI index.
Only that index and its linux/amd64 image manifest were read; their sizes and
SHA256s were verified. No config/layer/attestation/provenance/log blob was fetched.
The resulting identities are distinct:

| Role | SHA256 |
| --- | --- |
| Completed output index; supplied accepted engine `.Id` | `852c97dd37a51a4c6d5a933444e8f082594205aa8003d4616b6fc8ab689a7e0d` |
| linux/amd64 manifest | `990e94303a9e2f2b7201915568fb7869f0246cc478734ba5bbf234cc1acc0a11` |
| Image config descriptor | `33fe3a440d44b0ca312f52de6a53401c3e1628a3388b949e40a644b0b819e84b` |

The existing engine reports Docker29.10.1/API1.57, commit54cd494, containerd
snapshotter. Support did **not** re-inspect the owner's tag or ID. The migration
pins the supplied accepted engine `.Id`, independently matched to the native
completed output index, rather than substituting the config digest or inferring
an ID from a RepoDigest. Containerd image-store API implementations can expose
the target descriptor digest as `.Id`; the installed reader compares the literal
engine `.Id` on acceptance. The old source comment describing every `.Id` as a
config digest is over-specific; the executable predicate accepts the exact
engine-returned SHA256 and does not conflate the fields.

## Exact preserving recovery

`restore-known-output.py` is a Support-only, fixed-request migration, not an
installed/public command. `apply-restoration.py` hash-gates its source and native
proof against `reviewed.json`, requires deployment main, pins the installed
broker/module pair and checks the Kandev container identity before/after.

The migration validates the pre-existing exact immutable task/clone/project
claim and saved-model ownership envelope; neither model reference nor a `build`
key supplies ownership proof. It publishes **one** broker-owned mode0600 successful-output
receipt, with native proof hash/ref/config-index distinction, by atomic
no-replace linking. Any existing receipt, foreign/missing claim, foreign clone,
unsafe custody, changed model or incompatible installed pair stops the action.
Claim and saved-model bytes must remain unchanged. No new public import endpoint,
model/claim restoration, task/session change or service restart is introduced.

The installed per-request operations module remains
`d60ccdfeb55ea4cca64e10d1e68639f7f1cafa0d6705b8cb78be6a20bc6d59ee`.
The broker remains
`9514b692d9833fcc64c1978b7553c181c37e84625e645a0ad0521d4b2f298e91`.
Automatic Compose completion capture is still **source-only** (deployment
`cb2ba9121d75190bb140538ac6149787731a848e`, broker candidate SHA
`b449451499e7ee92163dc30719e94c533459ae28480a7de6c1cd6033729e5c15`).
No lifecycle candidate, binary overlay or long-running broker replacement is
activated by this recovery. The current installed reader can consume the restored
receipt independently of subsequent runtime-model replacement.

## Qualification and acceptance boundary

Sixteen native reader tests and eleven root synthetic migration tests pass. They cover
exact Ref/command bounds, missing/foreign/malformed/error records, distinct
config/index identities, private-field suppression, content hash/size/status
checks, exact claim/clone, no model-only bootstrap, no overwrite, custody and
unchanged model/claim. Prior real-wrapper/client/Compose-parser history tests
remain published in pass1; their fake engine proof is not owner acceptance.

Root's finite next action, after the restoration receipt is confirmed, is ONE
separately bound metadata acceptance in the unchanged owner session, from the
same cwd and restricted caller:

```sh
KANDEV_TASK_DOCKER_ACCESS=restricted docker image inspect kd_8355_connector346_php-test:dev365-b07-c054
```

Use the existing task-root/caller environment; do not create a new guard/token,
change models, use ID fallback or build merely to recover metadata. A denial or
identity mismatch stops that contract. No owner inspect/config/build/run was
performed by Support. Root separately owns any later derived-image build and
must account for the still-source-only Compose capture hook.

## Public implementation references

The diagnostic follows Buildx's existing Docker-driver transport and BuildKit's
read-only native schema, without exposing a new route to tasks:

- [Buildx0.38.1 Docker driver](https://github.com/docker/buildx/blob/v0.38.1/driver/docker/driver.go).
- [BuildKit0.34.0 history schema](https://github.com/moby/buildkit/blob/v0.34.0/api/services/control/control.proto).
- [Content Read schema](https://github.com/containerd/containerd/blob/v2.2.4/api/services/content/v1/content.proto).
- [Containerd image-store inspection implementation](https://github.com/moby/moby/blob/master/daemon/containerd/image_inspect.go) (general semantics reference, not an independently verified source checkout of installed commit54cd494).

The diagnostic uses only the existing host engine connection, exact native Ref
and linked manifest metadata. It does not select latest, change builder/context,
export broad bundles, request provenance/logs/source or perform owner commands.

## Custody preflight correction

The first restoration attempt stopped before any receipt/directory write because
it incorrectly expected root-owned broker metadata. Exact custody-only stat
readback confirms the installed broker uses UID1000 and mode0700/0600 private
storage, while its public scripts remain root-owned0555. The corrected migration
preserves that existing UID/GID on newly created receipt directories and files;
it does not change the task, caller, token or running broker identity. The
installed reader requires the receipt owner to equal its existing process UID.
No existing claim/model permission or owner is modified. The exact receipt was
confirmed absent before continuing; this is not a repeated owner operation.

The custody branch also exposed an unsupported `Path.chown` call before any
receipt publication. It was corrected to `os.chown(..., follow_symlinks=False)`
and qualified under actual root with UID1000 synthetic broker metadata: all
eleven tests pass, including existing receipt preservation. Only the exact
migration-created empty `successful-builds` directory requires custody settlement;
its inode, empty state, root ownership and0700 mode are checked, then its UID/GID
is restored to the existing parent broker custody. No existing owner resource or
claim/model is modified. The failed attempt stderr is retained as evidence.

## Applied result

The corrected recovery is applied. Receipt SHA256:
`517ab65da7da14ba04a34af76dd3cc109e2362b70e67d71c1489dbefd75d38c0`.
The claim and saved runtime model hashes remain exactly those recorded in
`restoration.json`; the Kandev container ID/image/start time are unchanged.
The actual installed receipt reader recognizes the restored exact output and
rechecks its existing immutable claim, with engine execution replaced by a hard
failure in the readback diagnostic. Three installed real-wrapper/client/parser
synthetic cases also pass (retained success, no-receipt denial, changed-output
denial). No live owner metadata acceptance or workload operation was performed.

The current base image is now supported by an independent historical-success
receipt instead of model-only registration. Root may bind its one unchanged-owner
metadata acceptance. This result does not claim that a later build captures
Compose output automatically; that forward hook remains source-only.
