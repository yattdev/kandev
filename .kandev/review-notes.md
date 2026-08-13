# Review notes

## Fixed during review

- `apps/backend/internal/agentctl/server/process/testmain_test.go:91` — `TestTestMainClearsInheritedGitLabEnv` only checked the re-exec'd child's exit code, but a `-test.run` filter that matches no test still exits 0 (`testing: warning: no tests to run`). Renaming or removing `TestAmbientGitLabEnvIsClearedForTests` would therefore have left this guard passing green while verifying nothing — the same vacuous-pass failure mode the test was added to eliminate. The filter and a `--- PASS:` assertion on the child's `-test.v` output now derive from one shared constant. (commit `b8698aa04`)

  Verified by negative control: pointing the constant at a non-existent test name makes the guard fail with `child did not run ...; the scrub was never exercised`, where previously it passed.

## Follow-up tasks created (out of scope for this PR)

None.

## Action required by author

- Two assumptions recorded in the QA HANDOFF section of the task plan are still awaiting an author veto; carry that section into the PR description as noted by QA.
