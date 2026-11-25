# Reserved legacy successful-build recovery

Request `a63f09f3-5213-4a87-add9-a983718059eb`. Design only; no trust grant,
receipt import, identity repinning, engine operation or activation is performed.

## Missing evidence and authority

The installed model registry has no independent successful-output history.
The affected owner reports a real prior zero-exit build and separately accepted
tag/ID reads. That is useful incident evidence, but the installed guard cannot
verify it from a runtime model or turn it into durable history automatically.
The repair never treats an agent-controlled plan, file, tag name, image label,
model reference or claim as a successful-build attestation.

The smallest required input is the original protected execution record for:

- Build365's exact authenticated task/clone/project/service/tag, bounded command,
  successful engine completion and its original result identity.
- The separate successful typed image identity read, keeping local engine `.Id`
  distinct from any registry manifest/RepoDigest value.
- A trusted attestation issuer and immutable evidence hash/reference usable by
  the reviewed host recovery operation. A task-written receipt without such
  provenance is not enough. Root's supplied native-receipt reference is not
  read or promoted to this authority by Support.

The exact legacy binding is the supplied task
`559695d6-ba34-42d0-b6d0-598a5b46ee0e`, session
`6b18155e-310e-42ea-81ff-7e82a2fe246d`, root/clone
`/data/tasks/8355-impl-menter-le_vmaz2aad`, project `kd_8355_connector346`,
service `php-test`, tag `kd_8355_connector346_php-test:dev365-b07-c054`.
The actual typed engine ID must come from the trusted proof, not a field guessed
from the request's combined image-ID/RepoDigest notation. No caller changes.

## Reviewed operation, not an agent-facing import

A separately authorized host recovery must validate that original successful
build proof, the unchanged immutable scope, and that exact current local tag's
identity. It may migrate one verified historical output to the same durable
receipt schema, bound to the evidence hash and reviewed authority. It must not
create/relabel/retag/build an image, rewrite the current runtime model, touch
DB/network/volume resources or grant broader image inspection.

No generic `recover --tag --id` tool is proposed: untrusted caller fields cannot
supply provenance. The proposed migration requires design review, explicit exact
legacy authority and independent positive/negative fixtures before installation
or execution. A mismatch, missing authentic proof, changed scope or changed tag
fails closed. Do not use direct SQL, hidden APIs, raw sockets or a different
actor as a Root workaround. This request supplies no such migration authority.

## Preserving exposure and acceptance

The forward history repair needs a reviewed broker/module pair; it cannot be
activated by copying only the hot operations module. Preserve all currently
installed fixes and in-flight work/leases under the normal installation gates.
No restart is authorized here. If exposure would require Kandev recreation,
use only the mandatory safe-deploy transaction after separate authority.

Once both preserving exposure and the exact proven legacy recovery are verified,
Root may bind one restricted `docker image inspect` operation on the same tag
in the unchanged owner session. That acceptance is pending, not performed by
Support's private fixtures. No new build or runtime operation is the next step.
