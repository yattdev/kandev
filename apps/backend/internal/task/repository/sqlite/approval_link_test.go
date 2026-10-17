package sqlite

import (
	"context"
	"encoding/json"
	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/auth/authn"
	kandevdb "github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/ownanswer"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestApprovalLinkSchemaAvailableAfterRepositoryRestart(t *testing.T) {
	r := newRepoForSessionTests(t)
	var count int
	err := r.db.GetContext(context.Background(), &count, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='own_answer_approval_link_grants'`)
	require.NoError(t, err)
	require.Equal(t, 1, count, "grant consumption must persist in the repository database")
	require.NoError(t, r.initSchema())
}

func TestApprovalLinkDirectNativeCommentsHaveNewAuthorization(t *testing.T) {
	r, ctx, b, _, a := approvalFixture(t)
	delete(a.Metadata, "queue_admission_ids")
	saveApprovalMessage(t, r, a)
	before := snapshotOwnAnswerBoundary(t, r)
	g, err := r.createApprovalLinkGrant(ctx, ownanswer.ApprovalLinkRequest{OwnAnswerBinding: b, OptionID: "opt-a"})
	require.NoError(t, err)
	require.Equal(t, ownanswer.ApprovalLinkPurpose, g.Purpose)
	_, changed, err := r.consumeApprovalLinkGrant(ctx, b, g.ID, "synthetic-operation", g.Incarnation)
	require.NoError(t, err)
	require.True(t, changed)
	after := snapshotOwnAnswerBoundary(t, r)
	_, changed, err = r.consumeApprovalLinkGrant(ctx, b, g.ID, "synthetic-operation", g.Incarnation)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, after, snapshotOwnAnswerBoundary(t, r))
	require.Equal(t, before["queued_messages"], after["queued_messages"])
	require.Equal(t, before["queue_admission_receipts"], after["queue_admission_receipts"])
}

type approvalLinkFixtureWriter interface {
	createApprovalLinkGrant(context.Context, ownanswer.ApprovalLinkRequest) (*ownanswer.ApprovalLinkGrant, error)
	consumeApprovalLinkGrant(context.Context, ownanswer.OwnAnswerBinding, string, string, string) ([]*models.Message, bool, error)
}

func TestApprovalLinkCreatorCanRevokeBeforeConsumption(t *testing.T) {
	r, ctx, b, _, _ := approvalFixture(t)
	g, err := r.createApprovalLinkGrant(ctx, ownanswer.ApprovalLinkRequest{OwnAnswerBinding: b, OptionID: "opt-a"})
	require.NoError(t, err)
	revoker, ok := any(r).(interface {
		revokeApprovalLinkGrant(context.Context, ownanswer.OwnAnswerBinding, string) error
	})
	require.True(t, ok, "creator must be able to revoke unconsumed authorization")
	before := snapshotOwnAnswerBoundary(t, r)
	foreign := authn.WithIdentity(ctx, authn.Identity{UserID: "foreign-admin", Role: authn.RoleAdmin, SessionID: "foreign-browser"})
	require.Error(t, revoker.revokeApprovalLinkGrant(foreign, b, g.ID))
	require.NoError(t, revoker.revokeApprovalLinkGrant(ctx, b, g.ID))
	require.NoError(t, revoker.revokeApprovalLinkGrant(ctx, b, g.ID))
	_, _, err = r.consumeApprovalLinkGrant(ctx, b, g.ID, "synthetic-operation", g.Incarnation)
	require.Error(t, err)
	require.Equal(t, before, snapshotOwnAnswerBoundary(t, r))
}

func TestApprovalLinkAtomicConsumptionAndIdempotency(t *testing.T) {
	r := newRepoForSessionTests(t)
	writer, ok := any(r).(approvalLinkFixtureWriter)
	require.True(t, ok, "repository must atomically persist grant consumption and the answer link")
	b, q, a := ownAnswerEvidence(t, r)
	a.Content = syntheticApprovalComments
	b.AnswerSHA256 = ownanswer.OwnAnswerDigest([]byte(a.Content))
	_, err := r.db.Exec(`UPDATE task_session_messages SET content=? WHERE id=?`, a.Content, a.ID)
	require.NoError(t, err)
	ownAnswerAdmission(t, r, a)
	seedOwnAnswerIndependentEvidence(t, r, q, a)
	ctx := seedApprovalBrowserAuthority(t, r)
	before := snapshotOwnAnswerBoundary(t, r)
	g, err := writer.createApprovalLinkGrant(ctx, ownanswer.ApprovalLinkRequest{OwnAnswerBinding: b, OptionID: "opt-a"})
	require.NoError(t, err)
	require.Equal(t, ownanswer.ApprovalLinkPurpose, g.Purpose)
	require.Equal(t, before, snapshotOwnAnswerBoundary(t, r), "issuing a grant cannot alter original records")
	rows, changed, err := writer.consumeApprovalLinkGrant(ctx, b, g.ID, "synthetic-operation", g.Incarnation)
	require.NoError(t, err)
	require.True(t, changed)
	require.Len(t, rows, 1)
	after := snapshotOwnAnswerBoundary(t, r)
	require.Equal(t, before["tasks"], after["tasks"])
	require.Equal(t, before["task_sessions"], after["task_sessions"])
	require.Equal(t, before["queued_messages"], after["queued_messages"])
	require.Equal(t, before["queue_admission_receipts"], after["queue_admission_receipts"])
	stored, err := r.GetMessage(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, syntheticApprovalComments, stored.Content)
	var databasePath string
	require.NoError(t, r.db.Get(&databasePath, `SELECT file FROM pragma_database_list WHERE name='main'`))
	require.NoError(t, r.db.Close())
	connection, err := kandevdb.OpenSQLite(databasePath)
	require.NoError(t, err)
	pool := sqlx.NewDb(connection, "sqlite3")
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	reopened, err := NewWithDB(pool, pool, nil)
	require.NoError(t, err)
	r = reopened
	_, changed, err = reopened.consumeApprovalLinkGrant(ctx, b, g.ID, "synthetic-operation", g.Incarnation)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, after, snapshotOwnAnswerBoundary(t, r))
	_, _, err = reopened.consumeApprovalLinkGrant(ctx, b, g.ID, "another-operation", g.Incarnation)
	require.Error(t, err)
	var raw string
	require.NoError(t, r.db.Get(&raw, `SELECT grant_json FROM own_answer_approval_link_grants WHERE id=?`, g.ID))
	var persisted ownanswer.ApprovalLinkGrant
	require.NoError(t, json.Unmarshal([]byte(raw), &persisted))
	require.WithinDuration(t, time.Now().Add(15*time.Minute), persisted.ExpiresAt, time.Minute)
}
