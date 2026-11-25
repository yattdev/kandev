> Historical first-pass report; superseded by escalation pass 1.

# Support 5e629b49 evidence and delivery status

Request `5e629b49-3b50-472f-85d6-54a8382fa515`, captured 2026-11-25.
No live target task records were read, and no board census or task lifecycle
action was performed. The 27 preserved IDs remain unresolved by Support.

## Source implemented; activation blocked

Deployment `main` commit `f040d5508b9db2ced7dd273f3ac28a759e649aa1` implements
one guarded read-only broker command and its synthetic tests. The exact
[deployment patch](deployment-lookup.patch) is published with this source
package. It changes only the broker, its new test and its contract document. The patch
uses zero context so it remains whitespace-clean as a nested Git artifact;
apply/check it with `git apply --unidiff-zero` against the verified broker base.
It preserves the existing authenticated transport and adds kernel-peer/exact
active-launch validation before Coordinator and same-workspace target checks.
The response includes typed plan presence; missing/foreign targets are masked
and deletion stays unproven.

Deployment source broker SHA256:
`064c4ef4869c7a80f9374eab567a73d90e6259b5ab1acb5377df8d62d87a64cc`.
The pre-change source broker matches the actual installed broker exactly:
`9514b692d9833fcc64c1978b7553c181c37e84625e645a0ad0521d4b2f298e91`.
This source delta is a preserving superset of that installed broker; no runtime
file was replaced. Deployment main has pre-existing unpublished commits, so
only this request's exact patch is published here; those unrelated commits are
not pushed as a side effect. The local deployment commit remains on main.

The read depends on platform-owned `kandev-test-worker.py` pinned to SHA256
`6a09ebeb96429f955546cf8b30616cd287cf92fe36156ea54f7d7b843ad78a96`.
Installed and checkout bytes match. The installed file is UID 0, mode 0555,
single-link, 145338 bytes. Its actual existing custody loader successfully
loaded this exact hash and confirmed `process`, `ancestry`, `metadata` are
callable. Metadata was not invoked; no worker service/policy/lease was started.

The new branch is not installed or loaded. Its dispatch runs inside the
existing long-running broker; a copied helper cannot activate that branch.
Restart/reload is prohibited by this request. Therefore no live command recipe
is presented as available and no caller-bound acceptance was attempted.

## Product source and current-reader characterization

Canonical `upstream/main` was fetched from
`https://github.com/kdlbs/kandev.git`, verified at
`8c4fd38852700a68272502cef39f46d122e7397c`, then checked out in an isolated
`support/task-lifecycle-5e629b49` worktree. The dirty primary source checkout and
all unrelated deployment changes were preserved. The official deployed-version
tag `v0.101.0` was separately resolved/fetched at
`f4484e17d99b805cd980ade543dbd4e2c5d6c4f8`.

Temporary tests run against canonical product source confirm:

- `Service.GetTask` returns active and retained archived rows, while
  `ListTasks` excludes archived rows.
- Missing and foreign-owner reads share `ErrTaskNotFound`; neither establishes
  deletion. The ordinary detail service can also reach a second workspace
  owned by the same user, so it cannot replace an exact Coordinator boundary.
- Authorized active/archived planless tasks return nil success in PlanService;
  missing/foreign and explicitly denied authorizers preserve typed service
  errors. Archive timestamps remain unchanged by the reads.
- `planws.GetError` maps missing/inaccessible task, explicit denial and a
  missing-plan sentinel to `INTERNAL_ERROR`. That mapping also exists in the
  deployed-version source tag. It is a demonstrated possible explanation for
  reported errors, not a diagnosis of any of the live 23 IDs.

Retained characterization source: [service](service-characterization.go.txt),
[mapper](planws-characterization.go.txt); [output](characterization-output.txt).
The temporary `*_test.go` files were removed before commit. No product Go or
permanent Go test file changed, and no native plan error fix is claimed.
The completed spec/plan/task-01 package records the precise repair.

Reproduction, in an isolated worktree at the canonical base only: copy each
characterization `.go.txt` beside its respective service/planws package as
`support_5e629b49_characterization_test.go`, run the command below, then remove
only those two temporary files. Do not copy them into a live runtime.

```sh
cd apps/backend
go test -p 2 -count=1 -v ./internal/task/service ./internal/task/planws -run '^TestSupport(LifecycleCharacterization|PlanReadErrorCharacterization)$'
```

Result: PASS in both packages; two top-level tests and three mapper subcases.
Go 1.26.0, `GOMAXPROCS=4`, normal Go cache access. The first sandboxed attempt
failed on read-only Go-cache writes; the authorized host rerun passed.
These tests use disposable fixture databases only; no provider or live API.

## Broker synthetic acceptance

`python3 tests/test-task-lifecycle.py`: **20 tests PASS**. See
[complete output](broker-tests-output.txt). Cases include active, archived,
missing/foreign, both foreign-workspace ownership cases, planless/present,
wrong/absent/mismatched/ambiguous/revoked caller proof, HMAC and kernel peer,
source custody/pin drift, PID/incarnation changes, bounded output and storage/
audit failure. Before/after fixture dumps verify preservation of tasks/plans
and retention fields; process/daemon command dispatch is prohibited in the test.

Real production launch-metadata matching and ancestry functions execute
against synthetic registered rows. The peer's own kernel identity is real;
the synthetic launch ends ancestry at the fixture process, avoiding unrelated
host-runner parents. Development file custody is substituted to load the same
source bytes. The Unix-handler/HMAC test isolates target dispatch; direct
metadata tests exercise the real reader. This is source integration evidence,
not an installed end-to-end owner/outer-guard acceptance.

Initial sandbox execution denied Unix socket send. A host rerun revealed that
unbounded fixture ancestry reached an unrelated unreadable runner parent; the
fixture was corrected to end at its own synthetic launch. All 20 cases then
passed; no production authorization check or assertion was weakened.

`python3 tests/test-agent-docker-broker.py`: PASS, including 16-test and 11-test
subordinate suites. [Output](broker-regression-output.txt). `git diff --check` passed.
Full deployment `test.sh` is outside this change: no image, compose, entrypoint
or installer file changed and no runtime-start validation was authorized.

## Installed/build identity, unchanged

Read-only public binary and image diagnostics, not task/private runtime reads:

| Component | Identity |
| --- | --- |
| Container | `8409b870b5ce55ce6e1a74203c0844d15085785a4a30fe944c5088c424983b57` |
| Image | `sha256:9a7b489e428439e090157cb96e482f8e21426ac6f75ae27402dee19fd3b3c8d4` |
| Start / restart count | `2026-11-18T16:39:05.618274966Z` / `0` |
| Version | `v0.101.0` |
| Backend SHA256 | `6d234280b9f0b757e6a2f12ab7d1708ba79cb3440faed9f0e28a1e6db855bf8b` |
| Agentctl SHA256 | `933d38c1551698c7bd761710d91d00dd2c30f2adaf992858a771609435aa0917` |
| Linux helper SHA256 | `98d7dba49bbbc0ef6571fa201915c052940ff79f4959903b508445a124d15675` |

The version/tag association is not a reproducible-build attestation. No new
binary was built, no overlay replaced, no image deployed and no service/broker
restarted. Any eventual binary deployment must first prove the full required
hotfix superset and use the mandatory safe-deploy gate. A binary replacement
is not necessary merely to expose the deployment-broker read.

## Caller, result limits and finite next action

Coordinator task `8ace1738-e080-47a8-85f4-f4d2e0f133db`, session
`0dfcb29c-813e-4751-a6b2-6e3e8071e352`, workspace
`25f53734-5aea-490e-b7ba-d5da6fe821dd` are supplied request bindings. Support
did not inspect those live task/session records, their private worktrees, the
27 missing IDs or the helper JSON. No caller identity was changed or accepted
on their behalf. G1 is not cleared by these synthetic tests.

Finite next action: submit a reviewed activation operation bound to deployment
commit `f040d5508b9db2ced7dd273f3ac28a759e649aa1`, candidate/old-broker hashes
above and the pinned validator, with explicit authority for the broker lifecycle
operation. After installed synthetic qualification, Root owns one bound read
from its unchanged session/workspace. Native MCP plan error repair separately
needs the explicit product implementation continuation required by the source
repository's design gate; see task 01. No polling or census is requested.
