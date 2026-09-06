package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/task/models"
)

func TestRecoverTaskSessionQueueCommitsReceiptAndReplaysExactSnapshot(t *testing.T) {
	repo, queueRepo := newSessionQueueRecoveryFixture(t)
	ctx := context.Background()
	first := insertSessionQueueRecoveryMessage(t, queueRepo, "first exact body")
	second := insertSessionQueueRecoveryMessage(t, queueRepo, "second exact body")

	result, err := repo.RecoverTaskSessionQueue(ctx, sessionQueueRecoveryScope())
	if err != nil {
		t.Fatalf("RecoverTaskSessionQueue: %v", err)
	}
	assertSessionQueueRecoveryResult(t, result, first, second)

	retry, err := repo.RecoverTaskSessionQueue(ctx, sessionQueueRecoveryScope())
	if err != nil {
		t.Fatalf("RecoverTaskSessionQueue retry: %v", err)
	}
	assertSessionQueueRecoveryResult(t, retry, first, second)
	if retry.Receipt.ID != result.Receipt.ID || retry.Receipt.SnapshotSHA256 != result.Receipt.SnapshotSHA256 {
		t.Fatalf("retry receipt = %#v, want stable identity/hash from %#v", retry.Receipt, result.Receipt)
	}
}

func TestRecoverTaskSessionQueueRejectsStalePrimaryWithoutMutation(t *testing.T) {
	repo, queueRepo := newSessionQueueRecoveryFixture(t)
	ctx := context.Background()
	queued := insertSessionQueueRecoveryMessage(t, queueRepo, "must remain")
	if err := repo.CreateTaskSession(ctx, &models.TaskSession{
		ID: "replacement", TaskID: "task-recovery", State: models.TaskSessionStateWaitingForInput,
	}); err != nil {
		t.Fatalf("CreateTaskSession replacement: %v", err)
	}
	if err := repo.SetSessionPrimary(ctx, "replacement"); err != nil {
		t.Fatalf("SetSessionPrimary replacement: %v", err)
	}

	_, err := repo.RecoverTaskSessionQueue(ctx, sessionQueueRecoveryScope())
	if !errors.Is(err, messagequeue.ErrQueueRecoveryUnauthorized) {
		t.Fatalf("RecoverTaskSessionQueue error = %v, want ErrQueueRecoveryUnauthorized", err)
	}
	assertQueueEntryRemainsAtSource(t, queueRepo, queued.ID)
}

func TestRecoverTaskSessionQueueRejectsResumedTargetWithoutMutation(t *testing.T) {
	repo, queueRepo := newSessionQueueRecoveryFixture(t)
	ctx := context.Background()
	queued := insertSessionQueueRecoveryMessage(t, queueRepo, "must remain")
	if err := repo.UpdateTaskSessionState(ctx, "source", models.TaskSessionStateRunning, ""); err != nil {
		t.Fatalf("resume source: %v", err)
	}

	_, err := repo.RecoverTaskSessionQueue(ctx, sessionQueueRecoveryScope())
	if !errors.Is(err, messagequeue.ErrQueueRecoveryTargetNotTerminal) {
		t.Fatalf("RecoverTaskSessionQueue error = %v, want ErrQueueRecoveryTargetNotTerminal", err)
	}
	assertQueueEntryRemainsAtSource(t, queueRepo, queued.ID)
}

func TestRecoverTaskSessionQueueRollsBackTransferWhenReceiptFails(t *testing.T) {
	repo, queueRepo := newSessionQueueRecoveryFixture(t)
	queued := insertSessionQueueRecoveryMessage(t, queueRepo, "must roll back")
	if _, err := repo.db.Exec(`
		CREATE TRIGGER fail_queue_recovery_receipt
		BEFORE INSERT ON queue_recovery_receipts
		BEGIN SELECT RAISE(ABORT, 'receipt failure'); END
	`); err != nil {
		t.Fatalf("create receipt failure trigger: %v", err)
	}

	if _, err := repo.RecoverTaskSessionQueue(context.Background(), sessionQueueRecoveryScope()); err == nil {
		t.Fatal("RecoverTaskSessionQueue error = nil, want receipt persistence failure")
	}
	assertQueueEntryRemainsAtSource(t, queueRepo, queued.ID)
	var receipts int
	if err := repo.db.Get(&receipts, `SELECT COUNT(*) FROM queue_recovery_receipts`); err != nil {
		t.Fatalf("count recovery receipts: %v", err)
	}
	if receipts != 0 {
		t.Fatalf("recovery receipt count = %d, want 0", receipts)
	}
}

func newSessionQueueRecoveryFixture(t *testing.T) (*Repository, messagequeue.Repository) {
	t.Helper()
	repo := newRepoForSessionTests(t)
	ctx := context.Background()
	if err := repo.CreateTask(ctx, &models.Task{
		ID: "task-recovery", WorkspaceID: "workspace-recovery", Title: "Queue recovery",
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	for _, session := range []*models.TaskSession{
		{ID: "destination", TaskID: "task-recovery", IsPrimary: true, State: models.TaskSessionStateWaitingForInput},
		{ID: "source", TaskID: "task-recovery", State: models.TaskSessionStateCompleted},
	} {
		if err := repo.CreateTaskSession(ctx, session); err != nil {
			t.Fatalf("CreateTaskSession(%s): %v", session.ID, err)
		}
	}
	queueRepo, err := messagequeue.NewSQLiteRepository(repo.db, repo.ro)
	if err != nil {
		t.Fatalf("NewSQLiteRepository: %v", err)
	}
	return repo, queueRepo
}

func sessionQueueRecoveryScope() messagequeue.QueueRecoveryScope {
	return messagequeue.QueueRecoveryScope{
		TaskID: "task-recovery", WorkspaceID: "workspace-recovery",
		SourceSessionID: "source", DestinationSessionID: "destination",
	}
}

func insertSessionQueueRecoveryMessage(
	t *testing.T,
	queueRepo messagequeue.Repository,
	content string,
) *messagequeue.QueuedMessage {
	t.Helper()
	entry := &messagequeue.QueuedMessage{
		SessionID: "source", TaskID: "task-recovery", Content: content, QueuedBy: messagequeue.QueuedByUser,
	}
	if err := queueRepo.Insert(context.Background(), entry, 10); err != nil {
		t.Fatalf("insert queue message: %v", err)
	}
	return entry
}

func assertSessionQueueRecoveryResult(
	t *testing.T,
	result *messagequeue.QueueRecoveryResult,
	want ...*messagequeue.QueuedMessage,
) {
	t.Helper()
	if result == nil || result.Receipt.ID == "" || result.Receipt.SnapshotSHA256 == "" {
		t.Fatalf("recovery result = %#v, want durable receipt", result)
	}
	if result.Receipt.EntryCount != len(want) || len(result.Entries) != len(want) {
		t.Fatalf("recovery result counts = receipt %d entries %d, want %d", result.Receipt.EntryCount, len(result.Entries), len(want))
	}
	for index := range want {
		if result.Entries[index].ID != want[index].ID || result.Entries[index].Content != want[index].Content ||
			result.Entries[index].SessionID != "source" || result.Entries[index].Position != want[index].Position {
			t.Fatalf("recovery entry %d = %#v, want identity/body/FIFO from %#v", index, result.Entries[index], want[index])
		}
	}
}

func assertQueueEntryRemainsAtSource(t *testing.T, queueRepo messagequeue.Repository, wantID string) {
	t.Helper()
	source, err := queueRepo.ListBySession(context.Background(), "source")
	if err != nil {
		t.Fatalf("list source queue: %v", err)
	}
	if len(source) != 1 || source[0].ID != wantID {
		t.Fatalf("source queue = %#v, want unchanged entry %s", source, wantID)
	}
	destination, err := queueRepo.ListBySession(context.Background(), "destination")
	if err != nil {
		t.Fatalf("list destination queue: %v", err)
	}
	if len(destination) != 0 {
		t.Fatalf("destination queue = %#v, want empty", destination)
	}
}
