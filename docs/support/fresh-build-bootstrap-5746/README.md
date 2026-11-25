# Restricted fresh dev-image bootstrap

Request `5746e621-90d7-484d-ab99-15e75e4ab6a5`.

**An existing restricted Compose route supports a fresh build without a prior
registered task image. No guard repair or installation is required.** The
reported `IMAGE_NOT_AUTHORIZED` is the expected historical-image denial and
must remain in force. This result qualifies admission and registration, not
an actual owner build, public image pin, dependency installation or vendor proof.

## Supported route and caller recipe

The same owner prepares a new local Dockerfile/context and a private overlay
under its exact existing root. Root must select the source service from the
original 719-byte model; `app` in the synthetic tests is not evidence of that
service's live name. Keep the original file byte-for-byte, root, project alias,
caller/session and DB/network/volume resources. The overlay changes only the
selected source service's image/build definition. It must not change the DB
service, its image, ports, binds, networks or volumes.

Once Root binds the completed recipe, frozen context, public fetching conditions
and one fresh tag, the one build invocation is:

```sh
test "$KANDEV_AGENT_DOCKER_ROOT" = /data/tasks/8355-impl-menter-le_vmaz2aad
cd "$KANDEV_AGENT_DOCKER_ROOT"
export COMPOSE_PROJECT_NAME=kd_8355_connector346
# OWNER_SELECTED_SERVICE is the exact service name fixed by Root's new contract.
KANDEV_TASK_DOCKER_ACCESS=restricted docker compose \
  --project-directory "$KANDEV_AGENT_DOCKER_ROOT" \
  -f "$KANDEV_AGENT_DOCKER_ROOT/.kandev/docker-compose.task.yml" \
  -f "$KANDEV_AGENT_DOCKER_ROOT/.kandev/fresh-build.approved.yml" \
  build --pull --no-cache "$OWNER_SELECTED_SERVICE"
```

This preserves the existing wrapper/client/token. Do not set guard markers,
change Docker context/socket/binary, unset restricted mode or use alternate
credentials. In the test only, an independent fixture supplies its own marker,
key, root and socket. Root's real caller inherits its existing guard framing.

The supported overlay shape, with **synthetic service/tag and unfilled pins**:

```yaml
services:
  selected-service:
    image: kd_8355_connector346_selected-service:fresh-bound-source-lock
    build:
      context: /data/tasks/8355-impl-menter-le_vmaz2aad/.kandev/approved-frozen-build-context
      dockerfile: Dockerfile
      pull: true
      args:
        PHP_BASE: "docker.io/library/php:<approved-8.4-variant>@sha256:<approved-manifest-digest>"
        COMPOSER_BASE: "docker.io/library/composer:<approved-version>@sha256:<approved-manifest-digest>"
```

Root substitutes the actual selected service, new scoped tag and verified public
pins before freezing the overlay; this template is not executable approval.
Use the exact `<alias>_<selected-service>:<new-tag>` prefix. The broker retains
a valid tag with that prefix; arbitrary/shared requested image names are
rewritten to `<alias>_<service>:latest`. Thus Root must supply the fresh scoped
tag explicitly to avoid unintentionally reusing `latest`. No tag, alias, claim
or model referring to the denied historical ID is a provenance substitute.

Omit `build.network` for the ordinary build network. Do not request host
networking, insecure entitlements, SSH/secret forwarding, external contexts,
additional contexts, cache imports/exports or extra output tags. `--pull` and
`pull: true` intentionally allow public base refresh/resolution; `--no-cache`
bypasses ordinary build-layer reuse, not the engine's content store or resolver.
Neither flag proves that particular bytes were fetched or used.

## Registration lifecycle and preserved denial

The installed broker performs these steps under existing root/cwd HMAC framing:

1. Parse the invocation's original files through the guarded Compose `config
   --format json` path; explicit project directory remains the claim directory.
2. Validate and translate the local build context and Dockerfile within the
   task root. Sanitize the output image to the selected project's service tag.
3. Atomically establish/check the project claim `(task_root, project_directory,
   project)` and save that sanitized model. A different task or clone cannot
   take the same claim. This requires **no historical image admission**.
4. Replay `compose --project-name <alias> -f <private-sanitized-model> build
   --pull --no-cache <one-service>`. The original files are freshly rendered;
   another saved model is not substituted. Only the selected build is requested.
   There is no `up`, DB command, network reservation, container start or cleanup.
5. Return the engine exit and output. The saved model/claim already exist even
   if the build fails. Registration therefore means authorized scope, not a
   successful build attestation or source/vendor provenance. A failed attempt
   must stop; it is not permission for another attempt or inspection.

A separate `config` invocation also saves registration before its parser replay;
it is not a read-only operation on the broker model registry. It is unnecessary
as an extra live probe: a separately bound `build` already renders/registers.

After a successful separately authorized build, the new scoped tag can resolve
the new engine `.Id` through the existing own-build metadata route. The typed-ID
reader admits only IDs resolving from this exact clone's registered build tags.
It never admits the old ID merely because the old image was cited in the base
model, or because a new build definition exists. Source tests explicitly retain
that denial. Public `FROM` resolution is performed by the build engine; it is
not raw agent permission to inspect public or shared local images.

Engine `.Id` is a local image-config identity. A public `name@sha256:...` pin is
a registry manifest identity. They are distinct; do not substitute either for
the other. The test's fake digests and fake IDs prove no real public pin.

## What Root must bind before any actual build

The earlier no-download/offline contract does not authorize this fresh public
bootstrap. Root's replacement contract must explicitly cover:

- The unchanged task `559695d6-ba34-42d0-b6d0-598a5b46ee0e`, session
  `6b18155e-310e-42ea-81ff-7e82a2fe246d`, root, alias, exact selected service,
  overlay/Dockerfile hashes, new output tag, platform and one build operation.
- The current source and lock binding: supplied snapshot manifest SHA256
  `0314c9f3be642e78345d095472a9e8840fd5da6f3929f4d689a388dc9055381a`,
  1110 files and lock SHA256
  `c054e56486278858ade664db438315d6bdbe7f050640807bdddf33fcd85908fd`.
  Support did not inspect or verify those private artifacts. The same owner
  prepares and rechecks the completed current-source build context. Context
  contents exclude `.env`, credentials, data, sockets, old vendor and irrelevant
  runtime material, with Dockerfile COPY and `.dockerignore` reviewed together.
- Real approved public PHP **8.4** base variant/platform and manifest pin, a
  pinned Composer tool stage if used, and any Node/system build stages needed
  by the new dev recipe. No stage, frontend, `COPY --from`, URL `ADD` or cache
  source may refer to a sibling, historical/private image or undeclared source.
- Explicit permission for the needed public registries, package repositories
  and lock-declared source/dist endpoints. Public fetching uses the ordinary
  engine builder and recipe's network. The broker is **not an endpoint egress
  allow-list** and does not parse Dockerfile instructions to enforce a list.
  Root must review recipe inputs accordingly; strict network enforcement would
  require a separately reviewed provisioning design, not a guessed flag.
- Exact PHP extensions/system packages and dev dependency installation from
  the unchanged lock (`composer install`, not `update`; no `--no-dev`). Disable
  application scripts/plugins unless separately bound. Composer/runtime
  platform checks remain enabled; do not use `--ignore-platform-reqs` to force
  acceptance. Include required test/dev packages and autoload generation in the
  new recipe, and define inert lock/version/dev-vendor/autoload metadata checks.
  Do not run application entrypoints, tests, providers or target calls as build
  proof. Registration or copying a vendor directory cannot establish the
  current source/lock/dev-vendor/autoload binding.
- The distinction between one build and subsequent new-image metadata/runtime
  acceptance. Capture the build exit and actual resolved inputs. Root separately
  binds any later owner metadata read, DB/app/test start or dependency proof.
  A successful admission test here is not any of those owner operations.

A possible dev-image foundation uses `ARG PHP_BASE`, `ARG COMPOSER_BASE`, a
public Composer tool stage, `FROM ${PHP_BASE}`, and `COPY --from=composer_tool
/usr/bin/composer /usr/local/bin/composer`, followed by the frozen source and
owner-approved extensions/locked dev install. This requires no historical own
image. Support deliberately does not invent the project's extensions, source
layout, scripts or vendor claims without reading its private source. The same
owner can prepare that concrete recipe under Root's separate contract.

If Root retains a strict zero-registry/zero-package-traffic requirement, all
base/frontend/tool and package artifacts must first be independently qualified
and provisioned through an already authorized mechanism. `network: none` limits
RUN networking; `pull: false`/`--pull=false` do not prohibit base/frontend
resolution. The installed route provides no general resolver firewall. No
unregistered image import/retag/provenance grant is proposed to fill that gap.

## Qualification and exact installed/source status

Both checkouts were inspected. This is the deployment's existing build policy;
no product-source or production guard correction is needed. Product checkout
HEAD is `cfd6bdf65ae6f631d7701c105791a099c0325cc3`; unrelated changes are preserved.
Deployment baseline is `0517d100df08579a763417b74f286b20a8d01b16`.

Installed broker SHA256:
`9514b692d9833fcc64c1978b7553c181c37e84625e645a0ad0521d4b2f298e91`.
Installed operations SHA256:
`d176a1794af1f5138a9b77d3594f09b9063277f6ea2183877891bb49c7abe7e4`.
The supplied `b0e635...` is the earlier image-repair revision. Readback observes
the later lifecycle-module revision from the separate resolved request; this
request neither installs, invokes, removes nor otherwise changes lifecycle
capability. Its image/build handlers remain the preserving image-fix superset.
Full wrapper/client/guard hashes and custody are in `installed-public-files.json`.
No binary/image build, installation, server reload or restart is performed.

`tests/test-fresh-build-bootstrap.py` passes 11 cases in installed mode, including
actual Compose **5.7.1** parsing, original two-file model merging, HMAC/root/cwd,
wrapper/client/private Unix transport, immutable claims, selected-service replay,
registered-output ID lookup and historical-image denial. The engine is mocked.
The parser/transport fixture uses an empty filesystem root and network namespace;
live `/data`, `/run` and `/app`, engine sockets and credential stores are absent.
Public scripts and private synthetic files only are available. Source mode
passes 10 cases with a normalized parser fixture and skips installed transport.
The real parser is covered by installed mode, not claimed from the source stub.

The first installed-fixture attempt omitted its synthetic guard-active marker.
The wrapper took its non-guarded path and the real Compose frontend failed on
an absent socket inside the empty namespace. No engine connected or build,
registry/package traffic occurred. The fixture was corrected to supply the
existing marker (as the real owner already has); production policy was unchanged.
`initial-fixture-framing.json` retains that failed attempt. Final source and
installed outputs are stored separately; no failing run is called acceptance.

**Finite next action:** Root issues one fresh, explicitly bound owner build
contract after the same owner freezes the new PHP8.4 dev recipe and Root permits
its required public provisioning. No owner retry or runtime action is authorized
by this Support response. The old image remains denied and all existing
DB/network/data/resources stay untouched. Support does no polling or census.

## Published evidence provenance

Deployment evidence commits: `92a7f9b` (suite/recipe) and `4b23f58` (raw
failure diagnostic encoded as JSON without losing its bytes). These commits
change only synthetic tests and documentation. The exact zero-context patch
is `deployment-evidence.patch`; check/apply with `git apply --unidiff-zero`
against deployment baseline `0517d100df08579a763417b74f286b20a8d01b16`.
The test source is retained verbatim as `test-fresh-build-bootstrap.py.txt`.
Run it as `tests/test-fresh-build-bootstrap.py` in the deployment checkout;
installed mode uses the public installed scripts in a private fixture.

This independent publication branch is based on canonical `upstream/main`
`9ee282c20a36b71d1d8ebae797865741bdc94a38`. It was fetched from
`https://github.com/kdlbs/kandev.git` and verified against remote main before
creating the clean worktree. No unrelated deployment history is pushed, and
the prior lifecycle branch and dirty primary source checkout are preserved.
No product source or native tests are changed by this evidence publication.
