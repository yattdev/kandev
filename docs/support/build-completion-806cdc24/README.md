# Build completion capture qualification

Request `806cdc24-d005-4605-b6ca-fd7ee61f21fe`.
Result: **BLOCKED. No capture repair installed.**

The existing reviewed source is not complete for this request's stronger
completion/output-binding and CAS/audit contract. Its Compose hook also cannot
be activated by replacing the permitted per-request module alone. No affected
owner operation or historical-receipt restoration was performed.

## Fresh installed boundary

Reviewed host Support read only the public installed broker, operations module,
wrapper, client and guard, plus the Kandev container's identity. No task roots,
models, claims, registrations, receipts, images, containers, source or data were
queried. Both source checkouts were inspected; unrelated edits were preserved.

The actual installed hashes match the prior a63 reader installation:

| File | Installed SHA256 |
| --- | --- |
| Broker | `9514b692d9833fcc64c1978b7553c181c37e84625e645a0ad0521d4b2f298e91` |
| Operations | `d60ccdfeb55ea4cca64e10d1e68639f7f1cafa0d6705b8cb78be6a20bc6d59ee` |
| Docker wrapper | `d05643a229d78f6cc724b1bc7a1a82904cd7f29ec40a0c61bd105caaff0bbd41` |
| Client | `fc18ec0fc01645ee8908f026d3e113a729ff815ca008803a1cc4a5d8b3f85133` |
| Guard | `73caa9ebd359e11b1047be861de0ddb2acc456fa166c450407a64af4f840b92f` |

The [installed dispatch excerpt](installed-dispatch-excerpt.py.txt) shows the
decisive condition: the operations module is loaded only when the request's
first argument is **not `compose`**. Compose rendering, replay and return remain
in the long-running broker. There is no installed completion callback into the
module on that path. Its helper for standalone builds is a different path and
does not install Compose capture.

The existing source broker at deployment commit
`cb2ba9121d75190bb140538ac6149787731a848e` adds the Compose completion hook. Its
SHA256 is `b449451499e7ee92163dc30719e94c533459ae28480a7de6c1cd6033729e5c15`.
The operations bytes at that commit exactly match the currently installed
`d60ccdf...` module. Copying those same bytes again cannot activate an absent
broker call. Replacing the broker file alone would not reload its already
loaded Python function. Adding another caller-facing build entrypoint or
altering the live broker's dispatch is not exposure of the existing reviewed
module repair. We did not introduce either workaround or change the caller,
credentials, wrapper, guard, command policy or running broker.

## Stricter output qualification fails

The [reviewed receipt helper](reviewed-receipt-source.py.txt) performs these
operations after the caller reports a zero build exit:

1. Revalidate the exact task/clone/project claim.
2. Resolve the scoped tag with engine `image inspect --format '{{json .Id}}'`.
3. Revalidate the claim and atomically replace the receipt for that tag.

It does not accept or validate a native completion/exporter record identifying
the output produced by that particular build. The receipt contains the observed
engine ID and timestamp, with no native build/completion identity. It also uses
unconditional `temporary.replace(path)`, rather than compare-and-swap against a
prior receipt generation. The receipt writer emits no capture-specific audit
event or repeat-operation identity. Atomic replacement is useful durability;
it does not supply those additional contracts.

[An independent strict fixture](run-completion-qualification.py.txt) characterizes
the missing output binding. Its private mocked build completes with output
`sha256:aaaa...`; before the following tag inspection, the same mock returns
`sha256:bbbb...` for the tag. The source candidate writes a successful-output
receipt for `bbbb...`. The assertion requiring either the completed `aaaa...`
output or a fail-closed result **fails**. The failing assertion was preserved,
not weakened or treated as a passing repair.

This is a synthetic completion-to-inspection race, not evidence that the real
owner's image was retargeted. A receipt written by this fixture is private test
state only. No historical or current task receipt is read, imported or created.

The local engine `.Id`, BuildKit output/index descriptor, selected platform
manifest and config descriptor digest are different identity roles. The code
correctly requests `.Id` rather than substituting a registry/config digest;
that fact alone does not connect the observed tag to this build's native output.
This investigation makes no rule that those digests are generally equal and
does not reopen the already-accepted Build365 historical proof.

## Verification and limits

Every engine result below is mocked. Installed parser and wrapper/client tests
run in private empty-root, no-network namespaces with synthetic caller keys,
roots, models, claims and receipts. The real Docker engine socket and live task
storage are unavailable inside those fixtures.

| Qualification | Result |
| --- | --- |
| Existing installed baseline characterization | 3 PASS |
| Existing installed retained-output reader | 3 PASS |
| Existing full source candidate suite | 26 PASS |
| New actual installed wrapper/client Compose dispatch qualification | 1 PASS: build and runtime config never enter the module; no automatic receipt; retained tag remains denied without a receipt |
| New strict source completed-output binding qualification | 1 FAIL: retargeted tag ID is recorded instead of completed output |

The existing source suite covers failed builds, dry-run/ambiguous selectors,
wrong scope/token, receipt custody, claim revocation, missing output identity and
post-receipt tag retargeting. Its passing cases do not cover the newly failing
completion-to-inspection boundary. Neither passing private tests nor a zero
mocked CLI exit are real successful-build or affected-owner acceptance evidence.

Exact commands and outputs are retained in the sibling `.txt` files and
[manifest](manifest.json). The strict candidate run exits 1 intentionally
because the requested contract is not met. No production test, receipt writer,
policy or installed file was modified to make that result pass.

## Preserved superset and rollback status

The installed operations bytes match the documented a63 pass1 module exactly.
That predecessor includes the 6b30 curated typed-image fix, lifecycle metadata
and typed-error work, volume-preservation boundaries, existing selected-worker
controls, and the decoupled successful-output reader/standalone capture.
Deployment history records these under `4686781`, `f7f7405`, `b8d4e3d`,
`681f606` and `cb2ba91`, with earlier volume/worker fixes retained in the same
module. No replacement superset was installed; all those bytes remain unchanged.
The historical a63 restoration is separate from module bytes and was neither
read nor repeated here.

Before/after container ID, image, start time and restart count match. Every
public installed file hash above also matches before/after. There was no
installation to roll back, no new backup needed, and no ownership/resource
cleanup. Existing backup and restoration state remain untouched.

## Exact remaining work and finite next action

The existing per-request installer cannot expose this Compose callback under
the unchanged installed dispatch. Additionally, the reviewed writer needs the
requested completed-output binding and CAS/audit/repeat-safety qualification
before it can be considered complete for this request.

The remaining owner is the **deployment-local broker maintainer**. A separately
scoped continuation must cover the broker completion-dispatch activation and
the stricter receipt contract, preserve the existing reader and all policy
boundaries, and pass the failing fixture plus native identity-role cases.
The current request authorizes only a no-restart per-request module exposure;
we did not infer permission for a broker reload/restart, new entrypoint, binary
or image deployment. No upstream-only classification is made.

There is no installed capture acceptance procedure to issue today. Root must
keep the derived build stopped. After a qualified repair is actually exposed,
Root can separately bind one original-owner selected-service build with its
then-current frozen source/lock/recipe, check its independently bound receipt,
and qualify retention across the admitted runtime-model change. The stale
1092-file context and historical receipt must not be reused as proof of that
new build. No owner command or retry is authorized by this report.
