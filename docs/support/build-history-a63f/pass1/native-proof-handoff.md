# One-record legacy proof handoff

Request `a63f09f3-5213-4a87-add9-a983718059eb`; reviewed design, not execution.

Required existing bindings:

- Task `559695d6-ba34-42d0-b6d0-598a5b46ee0e`, Work session
  `6b18155e-310e-42ea-81ff-7e82a2fe246d`, workspace
  `25f53734-5aea-490e-b7ba-d5da6fe821dd`.
- Root/clone `/data/tasks/8355-impl-menter-le_vmaz2aad`, project
  `kd_8355_connector346`, service `php-test`, output tag
  `kd_8355_connector346_php-test:dev365-b07-c054`.
- Original protected zero-exit build record in the supplied 21:19:54–21:22:26
  interval, its authenticated command/scope, and the separate accepted typed
  image identity result. Native-record time zones must be explicit; do not
  reinterpret the request's wall clock as UTC without evidence.
- Exact native BuildKit record Ref plus original builder/node identity, if the
  protected output supplies them. Neither Kandev receipt prefix `04f66a1f` nor
  engine image/digest text is a substitute. Do not select latest, use offsets,
  switch builder/context, infer a missing Ref or list unrelated records.

The next Support contract can permit one read of that exact protected execution
or native history record, projecting only completion/error presence, declared
output tag(s), engine config ID, record reference and the scope-binding evidence.
Existing reviewed host authority is sufficient for a properly bound read; no
agent socket/credential/path exposure or new Coordinator identity is needed.
Do not fetch image environment, logs, source attachments, build-arg values,
credentials or customer content. A future minimal native adapter should query
only the exact Ref and project required fields instead of exporting a broad
Desktop bundle. The current guarded owner CLI does not provide that adapter.

Successful legacy restoration must require an authentic engine-success/output
witness matched to the same existing immutable claim and exact tag/typed ID.
A current model, local-state cache, arbitrary task JSON, image label, timestamp
alone, or reported digest alone is insufficient. Engine `.Id` and registry
manifest/RepoDigest remain distinct. Missing/expired history preserves uncertainty;
it must not trigger a wider search, new build, model-only seeding or relaxed guard.

Only after that proof is verified under the separately bound operation may an
operator-reviewed exact-scope migration write one retained output receipt. The
new reader rechecks the immutable claim and current tag's ID before releasing
curated metadata, so a retargeted tag remains denied. No import or migration
is performed here and no public agent receipt-writing capability is added.

Root then binds ONE restricted exact-tag metadata acceptance in the unchanged
owner session. The installed reader alone is not permission to retry it.
Future automatic Compose capture remains a distinct source hook requiring normal
preserving broker exposure; no restart or service lifecycle is authorized here.
