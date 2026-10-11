package github

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	internaldb "github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/testutil"
	"github.com/stretchr/testify/require"
)

func newScopedStorePair(t *testing.T, postgres bool) (*Store, *Store) {
	t.Helper()
	var writer, reader *sqlx.DB
	if postgres {
		writer = testutil.OpenIsolatedPostgres(t, testutil.PostgresDSNFromEnv(t))
		var searchPath string
		require.NoError(t, writer.Get(&searchPath, `SHOW search_path`))
		raw, err := internaldb.OpenPostgres(testutil.PostgresDSNWithSearchPath(t, testutil.PostgresDSNFromEnv(t), searchPath), 1, 1)
		require.NoError(t, err)
		reader = sqlx.NewDb(raw, "pgx")
		t.Cleanup(func() { _ = reader.Close() })
	} else {
		dsn := filepath.Join(t.TempDir(), "interleaving.db") + "?_busy_timeout=10000&_journal_mode=WAL"
		var err error
		writer, err = sqlx.Open("sqlite3", dsn)
		require.NoError(t, err)
		t.Cleanup(func() { _ = writer.Close() })
		reader, err = sqlx.Open("sqlite3", dsn)
		require.NoError(t, err)
		t.Cleanup(func() { _ = reader.Close() })
	}
	writer.SetMaxOpenConns(1)
	reader.SetMaxOpenConns(4)
	timestamp := "DATETIME"
	if postgres {
		timestamp = "TIMESTAMPTZ"
	}
	require.NoError(t, createScopedPRPrerequisites(writer, timestamp))
	first, err := NewStore(writer, reader)
	require.NoError(t, err)
	second, err := NewStore(reader, reader)
	require.NoError(t, err)
	seedScopedPRURLTasks(t, first)
	return first, second
}

func TestScopedPRURLLegacyStalePrecheckSQLite(t *testing.T) {
	testScopedLegacyStalePrecheck(t, false, false)
}
func TestScopedPRURLLegacyStalePrecheckPostgres(t *testing.T) {
	testScopedLegacyStalePrecheck(t, true, false)
}
func testScopedLegacyStalePrecheck(t *testing.T, postgres bool, sameURL bool) {
	t.Helper()
	first, second := newScopedStorePair(t, postgres)
	ctx := scopedPRURLContext()
	client := NewMockClient()
	pr := *scopedPRFixture()
	pr.RepoOwner = "fork"
	pr.BaseRepoOwner = "fork"
	pr.HTMLURL = "https://github.com/fork/project/pull/42"
	pr.HeadSHA = strings.Repeat("b", 40)
	if sameURL {
		pr.RepoOwner = "upstream"
		pr.BaseRepoOwner = "upstream"
		pr.HTMLURL = scopedPRFixture().HTMLURL
	}
	client.AddRepos(pr.RepoOwner, []GitHubRepo{{Owner: pr.RepoOwner, Name: "project", FullName: pr.RepoOwner + "/project"}})
	client.SetPRDetail(pr.RepoOwner, "project", 42, &pr, nil)
	svc := newWorkspaceAuthenticatedTestService(t, client, first, "ws-1")
	events := &mockEventBus{}
	svc.eventBus = events
	// Reserve only the legacy writer's pool connection. Its independent reader
	// remains usable, so pool wait proves it has already performed the absent-slot read.
	held, err := first.db.Connx(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = held.Close() })
	before := first.db.Stats().WaitCount
	done := make(chan error, 1)
	go func() {
		_, err := svc.AssociateExistingPRByURLForWorkspace(ctx, "ws-1", "user", "child", "fork", pr.HTMLURL)
		done <- err
	}()
	require.Eventually(t, func() bool { return first.db.Stats().WaitCount > before }, 10*time.Second, time.Millisecond, "legacy writer must reach its transaction after the absent-slot read")
	strict, _, err := second.associateScopedPRURL(ctx, scopedPRRequest(), scopedPRFixture())
	require.NoError(t, err)
	require.NoError(t, held.Close())
	select {
	case err = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("legacy writer did not drain")
	}
	if sameURL {
		require.NoError(t, err)
	} else {
		require.Error(t, err, "stale ordinary precheck must not overwrite the strict association")
	}
	persisted, err := second.GetTaskPRByRepoAndNumber(ctx, "child", "fork", 42)
	require.NoError(t, err)
	require.Equal(t, strict.ID, persisted.ID)
	require.Equal(t, strict.PRURL, persisted.PRURL)
	require.Equal(t, strict.HeadSHA, persisted.HeadSHA)
	require.Zero(t, events.publishedCount())
}

func TestScopedPRURLSharedWriterFenceSQLite(t *testing.T)   { testScopedSharedWriterFence(t, false) }
func TestScopedPRURLSharedWriterFencePostgres(t *testing.T) { testScopedSharedWriterFence(t, true) }
func testScopedSharedWriterFence(t *testing.T, postgres bool) {
	t.Helper()
	for _, mode := range []string{"insert", "restore", "replace"} {
		for _, firstMode := range []string{"legacy first", "strict first"} {
			t.Run(mode+"/"+firstMode, func(t *testing.T) {
				first, second := newScopedStorePair(t, postgres)
				ctx := scopedPRURLContext()
				req := scopedPRRequest()
				pr := scopedPRFixture()
				status := &PRStatus{PR: pr, OutcomeFieldsPopulated: true}
				legacy := &TaskPR{ID: "legacy", TaskID: "other", WorkspaceID: "ws-1", RepositoryID: "fork", PRNumber: 42, Owner: "upstream", Repo: "project", PRURL: pr.HTMLURL, HeadSHA: pr.HeadSHA, State: pr.State}
				if mode == "restore" {
					require.NoError(t, first.CreateTaskPR(ctx, legacy))
					_, _, err := first.DetachTaskPR(ctx, legacy.ID)
					require.NoError(t, err)
				}
				if mode == "replace" {
					original := *legacy
					original.Owner = "second"
					original.PRURL = "https://github.com/second/project/pull/42"
					require.NoError(t, first.CreateTaskPR(ctx, &original))
				}
				tx, err := first.beginPRMutation(ctx)
				require.NoError(t, err)
				t.Cleanup(func() { _ = tx.Rollback() })
				if firstMode == "strict first" {
					require.NoError(t, first.scopedPRURLAdmissionTx(ctx, tx, req))
					_, _, err = first.scopedPRURLMutationTx(ctx, tx, req, pr)
				} else {
					switch mode {
					case "insert":
						err = insertTaskPR(ctx, tx, legacy)
					case "restore":
						_, err = restoreTaskPRTx(ctx, tx, "other", "fork", status)
					case "replace":
						_, err = replaceTaskPRTx(ctx, tx, legacy, status)
					}
				}
				require.NoError(t, err)
				done := make(chan error, 1)
				go func() {
					var err error
					if firstMode == "legacy first" {
						_, _, err = second.associateScopedPRURL(ctx, req, pr)
					} else {
						switch mode {
						case "insert":
							copy := *legacy
							err = second.CreateTaskPR(ctx, &copy)
						case "restore":
							_, err = second.RestoreTaskPR(ctx, "other", "fork", status)
						case "replace":
							copy := *legacy
							_, err = second.ReplaceTaskPR(ctx, &copy, status)
						}
					}
					done <- err
				}()
				require.Eventually(t, func() bool { return second.db.Stats().InUse > 0 || len(done) > 0 }, 10*time.Second, time.Millisecond, "contender must enter the held database boundary")
				select {
				case <-done:
					t.Fatal("contender escaped the shared transaction fence before commit")
				default:
				}
				require.NoError(t, tx.Commit())
				select {
				case err = <-done:
				case <-time.After(10 * time.Second):
					t.Fatal("contender did not drain")
				}
				rows, readErr := second.ListTaskPRsByPRNumber(ctx, "ws-1", "upstream", "project", 42)
				require.NoError(t, readErr)
				if firstMode == "legacy first" {
					require.Error(t, err)
					require.Len(t, rows, 1)
					require.Equal(t, "other", rows[0].TaskID)
				} else {
					require.NoError(t, err)
					require.Len(t, rows, 2, "intentional manual multi-links remain valid after strict commit")
				}
			})
		}
	}
}

func TestScopedPRURLConflictingSlotFenceSQLite(t *testing.T) {
	testScopedConflictingSlotFence(t, false)
}
func TestScopedPRURLConflictingSlotFencePostgres(t *testing.T) {
	testScopedConflictingSlotFence(t, true)
}
func testScopedConflictingSlotFence(t *testing.T, postgres bool) {
	t.Helper()
	first, second := newScopedStorePair(t, postgres)
	ctx := scopedPRURLContext()
	req := scopedPRRequest()
	pr := scopedPRFixture()
	tx, err := first.beginPRMutation(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	require.NoError(t, first.scopedPRURLAdmissionTx(ctx, tx, req))
	winner, _, err := first.scopedPRURLMutationTx(ctx, tx, req, pr)
	require.NoError(t, err)
	contender := *pr
	contender.RepoOwner = "second"
	contender.BaseRepoOwner = "second"
	contender.HTMLURL = "https://github.com/second/project/pull/42"
	otherReq := req
	otherReq.URL = contender.HTMLURL
	done := make(chan error, 1)
	go func() { _, _, err := second.associateScopedPRURL(ctx, otherReq, &contender); done <- err }()
	require.Eventually(t, func() bool { return second.db.Stats().InUse > 0 || len(done) > 0 }, 10*time.Second, time.Millisecond)
	select {
	case <-done:
		t.Fatal("conflicting claimant escaped fence")
	default:
	}
	require.NoError(t, tx.Commit())
	select {
	case err = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("conflicting claimant did not drain")
	}
	require.Error(t, err)
	persisted, err := second.GetTaskPRByRepoAndNumber(ctx, "child", "fork", 42)
	require.NoError(t, err)
	require.True(t, persisted.MatchesAssociationEvidence(winner))
}

func TestScopedPRURLLegacyIdenticalPrecheckSQLite(t *testing.T) {
	testScopedLegacyStalePrecheck(t, false, true)
}
func TestScopedPRURLLegacyIdenticalPrecheckPostgres(t *testing.T) {
	testScopedLegacyStalePrecheck(t, true, true)
}
