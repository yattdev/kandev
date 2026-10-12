package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/plancomments"
	"github.com/kandev/kandev/internal/testutil"
	v1 "github.com/kandev/kandev/pkg/api/v1"
	"github.com/stretchr/testify/require"
)

func TestTaskMessageReceiptRepositoryAtomicFences(t *testing.T) {
	verifyTaskMessageAtomicFences(t, newRepoForSessionTests(t))
}
func TestTaskMessageReceiptRepositoryPostgresAtomicFences(t *testing.T) {
	pool := testutil.OpenIsolatedPostgres(t, testutil.PostgresDSNFromEnv(t))
	repo, err := NewWithDB(pool, pool, nil)
	require.NoError(t, err)
	verifyTaskMessageAtomicFences(t, repo)
}

func verifyTaskMessageAtomicFences(t *testing.T, repo *Repository) {
	t.Helper()
	ctx := context.Background()
	seedForMsgTest(t, repo, "receipt-task", "receipt-session", "receipt-turn")
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "receipt-workspace", Name: "Synthetic"}))
	_, err := repo.db.ExecContext(ctx, repo.db.Rebind(`UPDATE tasks SET workspace_id=?, state=? WHERE id=?`), "receipt-workspace", v1.TaskStateReview, "receipt-task")
	require.NoError(t, err)
	session, err := repo.GetTaskSession(ctx, "receipt-session")
	require.NoError(t, err)
	queueRepo, err := messagequeue.NewSQLiteRepository(repo.db, repo.db)
	require.NoError(t, err)
	now := time.Now().UTC()
	makePair := func() (*models.Message, *messagequeue.QueuedMessage) {
		id := uuid.NewString()
		timestamp := now
		now = now.Add(time.Second)
		metadata := map[string]any{"sender_task_id": "receipt-sender", "sender_session_id": "receipt-sender-session", "task_message_intent_id": "synthetic-exact-intent", "task_message_target_step": "", "task_message_target_incarnation": session.QueueIncarnationID, plancomments.MetadataClientMessageFingerprint: "synthetic-fingerprint"}
		message := &models.Message{ID: id, TaskID: "receipt-task", TaskSessionID: "receipt-session", TurnID: "receipt-turn", Type: models.MessageTypeMessage, AuthorType: models.MessageAuthorUser, Content: "synthetic-exact-intent", Metadata: metadata, CreatedAt: timestamp, UpdatedAt: timestamp}
		queueMetadata := map[string]any{"sender_task_id": "receipt-sender", "sender_session_id": "receipt-sender-session", "task_message_intent_id": "synthetic-exact-intent", plancomments.MetadataClientMessageFingerprint: "synthetic-fingerprint", messagequeue.MetadataDurableTranscriptMessageID: id, "user_message_recorded": true}
		queued := &messagequeue.QueuedMessage{ID: id, TaskID: message.TaskID, SessionID: message.TaskSessionID, Content: message.Content, Metadata: queueMetadata, QueuedBy: messagequeue.QueuedByUser}
		return message, queued
	}
	message, queued := makePair()
	require.NoError(t, repo.CreateTaskMessageAndQueue(ctx, message, queued, nil, session.State, 1))
	require.True(t, queued.IsDurableDelivery(), "uses existing reserve/attempt/ack lifecycle")
	records, err := repo.FindOwnTaskMessageRecords(ctx, "receipt-workspace", "receipt-sender", "receipt-sender-session", "receipt-task", "receipt-session", "synthetic-exact-intent", "")
	require.NoError(t, err)
	require.Len(t, records, 2)
	for _, record := range records {
		require.Equal(t, message.ID, record.ID)
		require.NotEmpty(t, record.ContentSHA256)
	}
	for _, scope := range []struct{ workspace, sender, session string }{{"foreign", "receipt-sender", "receipt-sender-session"}, {"receipt-workspace", "peer", "receipt-sender-session"}, {"receipt-workspace", "receipt-sender", "peer"}} {
		hidden, err := repo.FindOwnTaskMessageRecords(ctx, scope.workspace, scope.sender, scope.session, "receipt-task", "receipt-session", "synthetic-exact-intent", "")
		require.NoError(t, err)
		require.Empty(t, hidden)
	}
	rejected, rejectedQueue := makePair()
	require.ErrorIs(t, repo.CreateTaskMessageAndQueue(ctx, rejected, rejectedQueue, nil, session.State, 1), messagequeue.ErrQueueFull)
	_, err = repo.GetMessage(ctx, rejected.ID)
	require.Error(t, err, "queue failure rolls back transcript")
	entries, err := queueRepo.ListBySession(ctx, "receipt-session")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, message.ID, entries[0].ID)
	changed, changedQueue := makePair()
	changed.Metadata["task_message_target_step"] = "different-human-step"
	require.Error(t, repo.CreateTaskMessageAndQueue(ctx, changed, changedQueue, nil, session.State, 10))
	_, err = repo.GetMessage(ctx, changed.ID)
	require.Error(t, err)
	task, err := repo.GetTask(ctx, "receipt-task")
	require.NoError(t, err)
	require.Equal(t, v1.TaskStateReview, task.State)
	require.NoError(t, repo.UpdateTaskSessionState(ctx, "receipt-session", models.TaskSessionStateCancelled, ""))
	stopped, stoppedQueue := makePair()
	require.Error(t, repo.CreateTaskMessageAndQueue(ctx, stopped, stoppedQueue, nil, session.State, 10))
	_, err = repo.GetMessage(ctx, stopped.ID)
	require.Error(t, err)
}
