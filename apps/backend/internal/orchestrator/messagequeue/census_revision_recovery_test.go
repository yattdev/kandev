package messagequeue

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCensusRevisionOrdinaryDispatchReceiptInvalidatesOtherEntry(t *testing.T) {
	ctx := context.Background()
	repo := newTestSQLiteRepo(t).(*sqliteRepository)
	identity := QueueSessionIdentity{
		TaskID: "task-dispatch-receipt", SessionID: "session-dispatch-receipt", SessionIncarnationID: "incarnation-dispatch-receipt",
	}
	seedQueueSessionIdentity(t, repo, identity)
	seedCensusExecution(t, repo, identity)
	require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
		ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "A", QueuedBy: QueuedByUser,
	}, DefaultMaxPerSession))
	require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
		ID: "entry-b", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "B", QueuedBy: QueuedByUser,
	}, DefaultMaxPerSession))
	reserved, enabled, err := repo.ReserveHeadIfAutoRunForSession(ctx, identity)
	require.NoError(t, err)
	require.True(t, enabled)
	require.NotNil(t, reserved)
	before, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	require.Len(t, before.Entries, 1)
	require.Equal(t, "entry-b", before.Entries[0].ID)
	require.NoError(t, repo.SetPendingQueueDispatchDelivery(ctx, reserved, "durable-v1", "submission-1", "payload-1"))
	withDelivery, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	require.Greater(t, withDelivery.Revision, before.Revision)
	require.NoError(t, repo.SetPendingQueueDispatchDelivery(ctx, reserved, "durable-v1", "submission-1", "payload-1"))
	replayedDelivery, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	require.Equal(t, withDelivery.Revision, replayedDelivery.Revision)
	require.NoError(t, repo.MarkPendingQueueDispatchAccepted(ctx, reserved))
	after, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	require.Greater(t, after.Revision, withDelivery.Revision)
	require.NoError(t, repo.MarkPendingQueueDispatchAccepted(ctx, reserved))
	replayed, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	require.Equal(t, after.Revision, replayed.Revision)
	require.NoError(t, repo.DeletePendingQueueDispatch(ctx, reserved))
	afterDelete, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	require.Greater(t, afterDelete.Revision, replayed.Revision)
	_, err = repo.DeleteClaimedPending(ctx, identity, "execution-1", "entry-b", before.Entries[0].ClaimSHA256)
	require.ErrorIs(t, err, ErrQueueChanged)
}

func TestCensusRevisionDispatchReceiptOverflowRollsBack(t *testing.T) {
	ctx := context.Background()
	repo := newTestSQLiteRepo(t).(*sqliteRepository)
	identity := QueueSessionIdentity{
		TaskID: "task-dispatch-overflow", SessionID: "session-dispatch-overflow", SessionIncarnationID: "incarnation-dispatch-overflow",
	}
	seedQueueSessionIdentity(t, repo, identity)
	seedCensusExecution(t, repo, identity)
	for _, id := range []string{"entry-a", "entry-b"} {
		require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
			ID: id, TaskID: identity.TaskID, SessionID: identity.SessionID, Content: id, QueuedBy: QueuedByUser,
		}, DefaultMaxPerSession))
	}
	reserved, _, err := repo.ReserveHeadIfAutoRunForSession(ctx, identity)
	require.NoError(t, err)
	require.NotNil(t, reserved)
	_, err = repo.db.ExecContext(ctx, repo.db.Rebind(`UPDATE queue_session_locks SET census_revision = ? WHERE session_id = ?`),
		int64(math.MaxInt64), identity.SessionID)
	require.NoError(t, err)
	before, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	beforeProtocol, beforeSubmissionID, beforePayloadHash := reserved.DeliverySubmission()
	var stored struct {
		ProtocolMode string `db:"protocol_mode"`
		SubmissionID string `db:"submission_id"`
		PayloadHash  string `db:"payload_hash"`
	}
	readStored := func() {
		require.NoError(t, repo.db.GetContext(ctx, &stored, repo.db.Rebind(`
			SELECT protocol_mode, submission_id, payload_hash FROM queue_dispatch_claims WHERE entry_id = ?
		`), reserved.ID))
	}
	readStored()
	storedBefore := stored
	require.ErrorIs(t, repo.SetPendingQueueDispatchDelivery(ctx, reserved, "durable-v1", "submission-1", "payload-1"),
		errCensusRevisionExhausted)
	after, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	require.Equal(t, before, after)
	protocol, submissionID, payloadHash := reserved.DeliverySubmission()
	require.Equal(t, beforeProtocol, protocol)
	require.Equal(t, beforeSubmissionID, submissionID)
	require.Equal(t, beforePayloadHash, payloadHash)
	readStored()
	require.Equal(t, storedBefore, stored)
}

func TestCensusRevisionSendNowReceiptInvalidatesOtherEntry(t *testing.T) {
	ctx := context.Background()
	repo := newTestSQLiteRepo(t).(*sqliteRepository)
	identity := QueueSessionIdentity{
		TaskID: "task-send-now-receipt", SessionID: "session-send-now-receipt", SessionIncarnationID: "incarnation-send-now-receipt",
	}
	seedQueueSessionIdentity(t, repo, identity)
	seedCensusExecution(t, repo, identity)
	source := &QueuedMessage{
		ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "A", QueuedBy: QueuedByUser,
	}
	require.NoError(t, repo.InsertForSession(ctx, identity, source, DefaultMaxPerSession))
	require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
		ID: "entry-b", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "B", QueuedBy: QueuedByUser,
	}, DefaultMaxPerSession))
	claim, err := repo.ClaimSendNow(ctx, identity.SessionID, []QueuedMessage{*source})
	require.NoError(t, err)
	before, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	require.Len(t, before.Entries, 1)
	require.Equal(t, "entry-b", before.Entries[0].ID)
	require.NoError(t, repo.SetPendingSendNowClaimDelivery(ctx, claim, "durable-v1", "submission-1", "payload-1"))
	withDelivery, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	require.Greater(t, withDelivery.Revision, before.Revision)
	require.NoError(t, repo.SetPendingSendNowClaimDelivery(ctx, claim, "durable-v1", "submission-1", "payload-1"))
	replayedDelivery, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	require.Equal(t, withDelivery.Revision, replayedDelivery.Revision)
	require.NoError(t, repo.MarkPendingSendNowClaimAccepted(ctx, claim))
	afterAccept, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	require.Greater(t, afterAccept.Revision, replayedDelivery.Revision)
	require.NoError(t, repo.MarkPendingSendNowClaimAccepted(ctx, claim))
	replayedAccept, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	require.Equal(t, afterAccept.Revision, replayedAccept.Revision)
	require.NoError(t, repo.DeletePendingSendNowClaim(ctx, claim))
	afterDelete, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	require.Greater(t, afterDelete.Revision, replayedAccept.Revision)
	_, err = repo.DeleteClaimedPending(ctx, identity, "execution-1", "entry-b", before.Entries[0].ClaimSHA256)
	require.ErrorIs(t, err, ErrQueueChanged)
}

func TestCensusRevisionSendNowReceiptOverflowRollsBack(t *testing.T) {
	ctx := context.Background()
	repo := newTestSQLiteRepo(t).(*sqliteRepository)
	identity := QueueSessionIdentity{
		TaskID: "task-send-now-overflow", SessionID: "session-send-now-overflow", SessionIncarnationID: "incarnation-send-now-overflow",
	}
	seedQueueSessionIdentity(t, repo, identity)
	seedCensusExecution(t, repo, identity)
	source := &QueuedMessage{
		ID: "entry-a", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "A", QueuedBy: QueuedByUser,
	}
	require.NoError(t, repo.InsertForSession(ctx, identity, source, DefaultMaxPerSession))
	require.NoError(t, repo.InsertForSession(ctx, identity, &QueuedMessage{
		ID: "entry-b", TaskID: identity.TaskID, SessionID: identity.SessionID, Content: "B", QueuedBy: QueuedByUser,
	}, DefaultMaxPerSession))
	claim, err := repo.ClaimSendNow(ctx, identity.SessionID, []QueuedMessage{*source})
	require.NoError(t, err)
	_, err = repo.db.ExecContext(ctx, repo.db.Rebind(`UPDATE queue_session_locks SET census_revision = ? WHERE session_id = ?`),
		int64(math.MaxInt64), identity.SessionID)
	require.NoError(t, err)
	before, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	beforeProtocol, beforeSubmissionID, beforePayloadHash := claim.DeliverySubmission()
	var storedBefore string
	require.NoError(t, repo.db.GetContext(ctx, &storedBefore, repo.db.Rebind(`
		SELECT claim_json FROM queue_send_now_claims WHERE session_id = ? AND claim_id = ?
	`), identity.SessionID, claim.ClaimID))
	require.ErrorIs(t, repo.SetPendingSendNowClaimDelivery(ctx, claim, "durable-v1", "submission-1", "payload-1"),
		errCensusRevisionExhausted)
	after, err := repo.ObservePendingCensus(ctx, identity, "execution-1", DefaultMaxPerSession)
	require.NoError(t, err)
	require.Equal(t, before, after)
	protocol, submissionID, payloadHash := claim.DeliverySubmission()
	require.Equal(t, beforeProtocol, protocol)
	require.Equal(t, beforeSubmissionID, submissionID)
	require.Equal(t, beforePayloadHash, payloadHash)
	var storedAfter string
	require.NoError(t, repo.db.GetContext(ctx, &storedAfter, repo.db.Rebind(`
		SELECT claim_json FROM queue_send_now_claims WHERE session_id = ? AND claim_id = ?
	`), identity.SessionID, claim.ClaimID))
	require.Equal(t, storedBefore, storedAfter)
}
