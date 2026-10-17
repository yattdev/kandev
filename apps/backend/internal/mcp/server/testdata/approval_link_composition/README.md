# Synthetic approval-link composition test

Run from the repository root with an unused task-owned artifact directory whose
parent already exists:

```sh
python3 apps/backend/internal/mcp/server/testdata/approval_link_composition/run.py \
  --artifacts /absolute/path/to/new-task-artifacts --race
```

The runner uses the preinstalled Go compiler offline (`GOTOOLCHAIN=local`,
`GOPROXY=off`, `GOSUMDB=off`, `-mod=readonly`). It writes its overlay, ordinary
source inventory, raw test log and hash receipt to that exclusive directory.
It never provisions dependencies, changes production files or runs an app.

The overlay maps three **absent** paths only:

| Testdata input | New compilation path under `apps/backend/` |
| --- | --- |
| `http_bridge.go.txt` | `internal/task/handlers/approval_link_composition_bridge.go` |
| `sqlite_bridge.go.txt` | `internal/task/repository/sqlite/approval_link_composition_bridge.go` |
| `composition_test.go.txt` | `internal/mcp/server/approval_link_composition_overlay_test.go` |

No existing function or file is replaced. Fixture bodies use `.go.txt` so no
ordinary package or changed-package hook treats the mixed package bodies as a
Go package. The two bridges are under `testdata`,
excluded from normal GoFiles/CgoFiles/imports/exports, and require no build tags
or runtime selectors. The runner verifies that normal inventories exclude them,
that the paths remain absent, and that normal source hashes stay unchanged.

The only production refactor extracts a private HTTP worker. Its ordinary route
always passes the unchanged compiled `ApprovalLinkCase()`. SQLite's exported
issue/consume selectors remain unchanged. Only the additive test compilation
can invoke the private HTTP worker with a synthetic binding and the private
SQLite transaction workers through the fixture adapter. The fixture adapter
enforces that binding and trusted execution identity; all ordinary authority,
evidence, transaction, commit-fence and replay checks still run.

The test creates only invented TempDir SQLite rows. A real auth service issues
a synthetic browser session; real middleware and task authorization feed the
HTTP worker and SQLite grant insertion. The registered MCP tool routes through
the ordinary dispatcher and backend handler, task-owner scope resolver, trusted
fixture execution context and SQLite consumption worker. It checks anonymous,
forged-cookie, PAT, foreign-owner and invalid-option denials; verifies ordinary
HTTP/SQLite selectors reject the same synthetic case; issues and consumes a
grant; forces a post-CAS rollback; verifies replay and conflicting-operation
denial; and checks preservation of original bodies, unrelated messages,
questions, task/session/executor/turn rows, FIFO and admission receipts.

Synthetic policy injection proves shared implementation composition. It does
**not** prove that invented input satisfies the compiled original-message hash
preimages, reproduce historical provenance, or authorize a live repair. The
publication bus is intentionally absent, so the backend reports publication
pending while the test asserts durable local state. PostgreSQL and a running
application are outside this test.
