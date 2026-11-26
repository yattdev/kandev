# Instruction compatibility diagnosis: ec7a40f4

Request: `ec7a40f4-d685-4c6f-99e7-a81102766b7b`.
Scope: source/configuration diagnosis and private synthetic qualification only.
This is a changed acceptance follow-up to `5fb40e77`, not another attempt to
install the metadata or a claim that the guard denied a write.

## Findings

The two reported outcomes require different evidence. The first is an agent's
unsubstantiated provenance objection; the second is an unperformed storage
qualification. Neither proves a native filesystem denial or an actual
higher-priority instruction prohibiting the already-authorized metadata.
The original stopped owners remain stopped. This package contains no permission
to resend, install, launch, change attribution, or change their configuration.

### Authorization recognition

In the inspected platform source, `handleMessageTask` in
`apps/backend/internal/mcp/handlers/handlers.go:3080` accepts task/session,
sender task/session, prompt, delivery mode, parent-question reply and send-operation
identity. It wraps the prompt through `wrapAgentMessage` at line 3187.
The wrapper at `apps/backend/internal/mcp/handlers/wrap_agent_message.go:60`
identifies agent input as peer input and allows refusal. An explicit target
session is preserved by `message_target_session.go`; queued delivery does not
change the sender's authority category. `SENT` establishes delivery status,
not recipient agreement, a successful operation or a verified Human grant.

The wrapper contains **no metadata-specific requirement for fresh User
verification**. Conversely, these request fields and the attribution metadata
do not carry an authenticated pointer to an existing Human delegation record,
its allowlist or its applicability to an instruction-file operation.
Parent-question replies and operation IDs serve other contracts; neither is a
general delegated filesystem permission. Supplying an approval claim in the
body does not add such a platform capability. This absence is not itself a
mandatory verification rule: an agent can act on peer requests within its
already-established scope, as the other reported owners did.

Therefore the first fresh refusal cannot be classified as an actual overriding
policy or a proven platform authorization defect. Its actionable category is
**recipient authority recognition, with restriction provenance unestablished**.
No source/category/scope for an actual conflicting loaded instruction was
supplied. We did not read either target's instructions, conversation, source or
runtime to invent that evidence. The application attribution policy above is
the concrete source we can establish; it is not a new instruction-file ban and
does not demonstrate that the metadata objective needs Human approval again.

There is no inspected supported flag that requires a recipient to accept a
delegated exception while retaining this caller, sender attribution, profile
and trust contract. Repeating the same approval claim is not a qualification
method. Changing the message into a purported direct User/system instruction,
changing roles or assigning another writer is outside this request.

### Owner-native storage qualification

The deployment guard has an existing source-supported observation path:

* For physical cwd below `/data/tasks`, `managed_child` selects its first task
  component. The guard canonicalizes and checks that root and cwd containment.
* When its project broker environment is available, it exports that exact root
  as `KANDEV_AGENT_DOCKER_ROOT`. That variable can be absent for a reason other
  than absent storage; it is a hint, not a signed scope grant.
* The namespace first hides the task parent and binds the exact selected root
  writable. `pwd` can therefore be a nested checkout even when a separate outer
  task root exists. Neither historical cwd in this incident qualifies its parent.
* The normal `git` wrapper already admits bounded `rev-parse` discovery reads
  directly when Git administration is read-only. The existing
  `tests/test-agent-git-readonly.py` passes. No Docker command, broker/registry
  lookup, alternate Git binary or fresh guard is required by the allowed reads.

[The exact diagnostic reference](owner-qualification-reference.py.txt) performs
the following sequence in the owner's unchanged native shell. It is reference
text for a separately bound operation, not an installed helper or the Host
implementation of the Coordinator loader workflow.

1. Read only the nonsecret guard root hint and physical current directory.
   Reject missing scope, symlink aliases, cwd escape and unknown Git context
   overrides. Preserve the guard's two existing maintenance settings; do not
   unset or replace any caller context. No token is read or printed.
2. Open that exact directory. Match its `/proc/self/fdinfo/<fd>` mount ID to
   `/proc/self/mountinfo`, requiring that exact directory to be the mountpoint,
   VFS `rw`, and a non-read-only `fstatvfs`. Return only that mount's ID and the
   owner's root identity, not mount sources or other mount records.
3. Use the existing native `git --no-pager --no-optional-locks -C ... rev-parse`
   reads. Require the physical root to be a strict ancestor of the current
   physical checkout, and require positively recognized non-repository status
   at the root itself. Generic Git exit 128 is **unknown**, not absence.
   An ignored directory inside the checkout fails. So does an outer directory
   that is itself a Git repository. No parent is guessed or searched for as an
   alternative writable scope.
4. If Root's separate contract includes actual writability qualification,
   create a **zero-byte anonymous inode** with `O_TMPFILE` through that already
   open directory descriptor. Verify zero links and zero size, then close it.
   This is an actual transient filesystem operation, not a read-only probe;
   it creates no named path or bootstrap content. Unsupported or denied means
   unqualified. There is no named-file fallback, chmod or cleanup operation.
5. Recheck root device/inode, descriptor mount and Git relation. Emit one
   content-free JSON observation. Four bounded Git reads are used, each with a
   ten-second deadline; there is no polling, network call, replay or retry.

The reference is suitable for embedding as the stdin body of the existing
owner-native `/usr/bin/python3 -` invocation, with the ordinary `git` on PATH.
Do not install the file, change PATH, substitute a binary or set a root to make
it pass. Root must bind the exact text/hash and original owner identity before
any real execution. No real owner execution occurred in this investigation.

A positive observation establishes the current owner's local kernel-visible
storage facts. It is **not** a cryptographic task/session attestation, a durable
lease, permission for every checkout file, or metadata installation acceptance.
It deliberately emits `authenticated_task_session_grant: false`.
If the hint or these kernel/Git observations are unavailable, retain a typed
unknown. The missing reusable platform capability would be a nonsecret,
authenticated **own-scope descriptor**, exposing only the current session's
canonical scope and checkout relationship. Do not infer absence or fall back
to an ignored checkout directory or an assumed outer pathname.

The existing agentctl `/api/v1/workspace/root-files` reader is a different
contract. Its `WorkspaceRootFiles` schema returns support status, file listings,
truncation and repository destinations. It neither supplies this descriptor nor
proves writability, and it is not an exposed own-root MCP qualification tool in
the inspected catalog. We inspected its source without invoking the endpoint
or using a listing of target files as a proxy.

## Private verification

[The private fixture](private-fixture.py.txt) uses temporary synthetic roots,
repositories and no-network mount namespaces. Production Git-wrapper dispatch
is copied unchanged except for the existing test seam replacing its broker
executable with a fail-and-record tripwire. No live credential, task, engine,
database, registry, provider or target filesystem is used.

[The receipt](private-fixture-results.json) records 16 passing executions:
14 positive/negative cases, including a filesystem-boundary root and rejected
generic Git errors, plus two executions of the exact reference text. The
unsupported and permission-denied anonymous-inode cases inject errors in the
private harness only; the reference contains no fixture hooks. There were zero
broker calls and zero named-file/content changes. This does not claim every
filesystem timestamp is immutable during an anonymous-inode probe.

The existing read-only Git regression also passed separately. No new Go code
was compiled, model behavior simulated, or affected-owner acceptance claimed.

## Bounded compatibility proposal, separate disposition only

The existing instruction issuer owns resolving applicability of the already
approved October 14 exception; the platform maintainer owns any reusable
recognition mechanism. The missing decision is how the existing authorization
is verifiably represented to these original recipients, **not whether to
approve the metadata objective again**.

If product work is selected later, the smallest contract to evaluate is an
authenticated reference to an existing approval/delegation record, coupled to
the unchanged sender and exact original recipient task/session/workspace/profile,
allowlisted instruction paths and managed operation/content hashes. It must
preserve nonmanaged text, product/index boundaries, revocation and bounded
operation identity. It must not create permissions from agent text, override
actual higher-priority rules or relabel a peer as a User. A recipient must be
able to return a meaningful distinction between missing provenance, a policy
conflict summarized by source/category/scope, unavailable own scope, and a
native technical denial. No hidden prompt quotation is required.

This is a proposal boundary, not a chosen design, implementation or new trust
grant. The source review cannot guarantee model compliance or conclude that no
other loaded restriction exists. Until that compatibility disposition is made,
do not repeat the failed metadata operation. Root can separately bind the
content-free storage qualification if useful, then retain its own managed-block,
nonmanaged-text and product/index checks for any later authorized installation.

## Source and installed status

Both checkouts were inspected. Platform code is pinned to canonical
`upstream/main` revision `f675a554a5b984cdb6d141c6e9f6a91e50d33694` in the
existing Support publication worktree; its parent before this diagnosis is
`e6f77c295398788aab8eaf23382b71b177434cd0` (prior diagnostic documents).
Primary source checkout HEAD is `cfd6bdf65ae6f631d7701c105791a099c0325cc3`;
its unrelated edits were preserved. Deployment checkout is `main` at
`57141859275709e42815cfbbf70b7b7dc6397bc9`; its unrelated dirty files were
preserved, including the guard. Exact inspected file hashes are in
[source-manifest.json](source-manifest.json).

This pass is **source-qualified only**. It did not read installed files through
the live service, rebuild a candidate, install anything, alter a prompt or
policy, or restart a process. It makes no fresh installed-byte-equivalence
claim and no statement that either owner has the required root. Only diagnostic
documents and private-fixture evidence are published.
