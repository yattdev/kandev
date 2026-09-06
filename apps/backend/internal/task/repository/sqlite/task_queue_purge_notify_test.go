package sqlite

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/common/logger"
	taskdb "github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/task/models"
)

// TestArchiveTaskNotifiesQueuePurgeAfterCommit proves the post-commit purge
// notifier fires after ArchiveTask empties queued_messages. Live badge zeroing
// depends on this hook publishing message.queue.status_changed.
func TestArchiveTaskNotifiesQueuePurgeAfterCommit(t *testing.T) {
	repo := newRepoForArchiveTests(t, "task-queue-purge-notify")
	ctx := context.Background()
	seedLiveSessionForQueue(t, repo, "session-1", "task-queue-purge-notify")

	mqRepo, err := messagequeue.NewSQLiteRepository(repo.db, repo.db)
	if err != nil {
		t.Fatalf("NewSQLiteRepository: %v", err)
	}
	log, err := logger.NewLogger(logger.LoggingConfig{Level: "error", Format: "console"})
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	queue := messagequeue.NewService(mqRepo, messagequeue.DefaultMaxPerSession, log)
	if _, err := queue.QueueMessage(ctx, "session-1", "task-queue-purge-notify", "follow up", "", "user", false, nil); err != nil {
		t.Fatalf("QueueMessage: %v", err)
	}
	if got, err := queue.CountPendingByTask(ctx, "task-queue-purge-notify"); err != nil || got != 1 {
		t.Fatalf("pending before archive = %d err=%v, want 1", got, err)
	}

	var notified atomic.Int32
	var notifiedTask string
	repo.SetTaskQueuePurgeNotifier(func(_ context.Context, taskID string) {
		notified.Add(1)
		notifiedTask = taskID
	})

	if err := repo.ArchiveTask(ctx, "task-queue-purge-notify"); err != nil {
		t.Fatalf("ArchiveTask: %v", err)
	}

	if notified.Load() != 1 {
		t.Fatalf("purge notifier calls = %d, want 1 after ArchiveTask", notified.Load())
	}
	if notifiedTask != "task-queue-purge-notify" {
		t.Fatalf("notified task_id = %q, want task-queue-purge-notify", notifiedTask)
	}
	if got, err := queue.CountPendingByTask(ctx, "task-queue-purge-notify"); err != nil || got != 0 {
		t.Fatalf("pending after archive = %d err=%v, want 0", got, err)
	}

	task, err := repo.GetTask(ctx, "task-queue-purge-notify")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.ArchivedAt == nil {
		t.Fatal("ArchivedAt = nil after archive")
	}
}

func TestDeleteTaskNotifiesQueuePurgeAfterCommit(t *testing.T) {
	repo := newRepoForArchiveTests(t, "task-delete-purge-notify")
	ctx := context.Background()
	seedLiveSessionForQueue(t, repo, "session-1", "task-delete-purge-notify")

	mqRepo, err := messagequeue.NewSQLiteRepository(repo.db, repo.db)
	if err != nil {
		t.Fatalf("NewSQLiteRepository: %v", err)
	}
	log, err := logger.NewLogger(logger.LoggingConfig{Level: "error", Format: "console"})
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	queue := messagequeue.NewService(mqRepo, messagequeue.DefaultMaxPerSession, log)
	if _, err := queue.QueueMessage(ctx, "session-1", "task-delete-purge-notify", "follow up", "", "user", false, nil); err != nil {
		t.Fatalf("QueueMessage: %v", err)
	}

	var notified atomic.Int32
	repo.SetTaskQueuePurgeNotifier(func(_ context.Context, taskID string) {
		if taskID == "task-delete-purge-notify" {
			notified.Add(1)
		}
	})

	if err := repo.DeleteTask(ctx, "task-delete-purge-notify"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}
	if notified.Load() != 1 {
		t.Fatalf("purge notifier calls = %d, want 1 after DeleteTask", notified.Load())
	}
	if _, err := repo.GetTask(ctx, "task-delete-purge-notify"); err == nil {
		t.Fatal("expected task gone after DeleteTask")
	}
}

func TestDeleteTaskSessionRejectsPendingQueueAndPreservesEntries(t *testing.T) {
	// Session cleanup must never turn a pending entry into an orphan or silently
	// dispose it. The session row and its exact FIFO remain recoverable together.
	repo := newRepoForArchiveTests(t, "task-session-queue-purge")
	ctx := context.Background()

	seedLiveSessionForQueue(t, repo, "session-1", "task-session-queue-purge")
	seedLiveSessionForQueue(t, repo, "session-drop", "task-session-queue-purge")

	mqRepo, err := messagequeue.NewSQLiteRepository(repo.db, repo.db)
	if err != nil {
		t.Fatalf("NewSQLiteRepository: %v", err)
	}
	log, err := logger.NewLogger(logger.LoggingConfig{Level: "error", Format: "console"})
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	queue := messagequeue.NewService(mqRepo, messagequeue.DefaultMaxPerSession, log)
	if _, err := queue.QueueMessage(ctx, "session-drop", "task-session-queue-purge", "orphan me", "", "user", false, nil); err != nil {
		t.Fatalf("QueueMessage drop: %v", err)
	}
	if _, err := queue.QueueMessage(ctx, "session-1", "task-session-queue-purge", "keep me", "", "user", false, nil); err != nil {
		t.Fatalf("QueueMessage keep: %v", err)
	}

	if err := repo.DeleteTaskSession(ctx, "session-drop"); err == nil {
		t.Fatal("DeleteTaskSession succeeded with a pending queue entry")
	} else if !strings.Contains(err.Error(), "pending queue") {
		t.Fatalf("DeleteTaskSession error = %q, want actionable pending queue error", err)
	}
	if _, err := repo.GetTaskSession(ctx, "session-drop"); err != nil {
		t.Fatalf("session-drop was removed after rejected delete: %v", err)
	}
	status := queue.GetStatus(ctx, "session-drop")
	if status.Count != 1 || status.Entries[0].Content != "orphan me" {
		t.Fatalf("session-drop queue = %+v, want original entry preserved", status.Entries)
	}
	if got, err := queue.CountPendingByTask(ctx, "task-session-queue-purge"); err != nil || got != 2 {
		t.Fatalf("pending after rejected session delete = %d err=%v, want 2", got, err)
	}
}

func TestQueueAdmissionRejectsDeletedSession(t *testing.T) {
	repo := newRepoForArchiveTests(t, "task-deleted-session-admission")
	ctx := context.Background()
	seedLiveSessionForQueue(t, repo, "session-deleted", "task-deleted-session-admission")

	mqRepo, err := messagequeue.NewSQLiteRepository(repo.db, repo.db)
	if err != nil {
		t.Fatalf("NewSQLiteRepository: %v", err)
	}
	log, err := logger.NewLogger(logger.LoggingConfig{Level: "error", Format: "console"})
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	queue := messagequeue.NewService(mqRepo, messagequeue.DefaultMaxPerSession, log)

	if err := repo.DeleteTaskSession(ctx, "session-deleted"); err != nil {
		t.Fatalf("DeleteTaskSession: %v", err)
	}
	if _, err := queue.QueueMessage(
		ctx, "session-deleted", "task-deleted-session-admission", "late message", "", "user", false, nil,
	); err == nil {
		t.Fatal("QueueMessage admitted an orphan entry after session deletion")
	}
	if got, err := queue.CountPendingByTask(ctx, "task-deleted-session-admission"); err != nil || got != 0 {
		t.Fatalf("pending after rejected admission = %d err=%v, want 0", got, err)
	}
}

func TestQueueAdmissionAndSessionDeleteRaceNeverOrphansOrDrops(t *testing.T) {
	repo := newRepoForArchiveTests(t, "task-session-delete-admission-race")
	ctx := context.Background()
	mqRepo, err := messagequeue.NewSQLiteRepository(repo.db, repo.db)
	if err != nil {
		t.Fatalf("NewSQLiteRepository: %v", err)
	}
	log, err := logger.NewLogger(logger.LoggingConfig{Level: "error", Format: "console"})
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	queue := messagequeue.NewService(mqRepo, messagequeue.DefaultMaxPerSession, log)
	queue.SetAutoMergeEnabled(false)

	for iteration := 0; iteration < 20; iteration++ {
		sessionID := "session-race-" + strconv.Itoa(iteration)
		seedLiveSessionForQueue(t, repo, sessionID, "task-session-delete-admission-race")
		start := make(chan struct{})
		var wait sync.WaitGroup
		wait.Add(2)
		var queueErr, deleteErr error
		go func() {
			defer wait.Done()
			<-start
			_, queueErr = queue.QueueMessage(ctx, sessionID, "task-session-delete-admission-race", "exact race body", "", "user", false, nil)
		}()
		go func() {
			defer wait.Done()
			<-start
			deleteErr = repo.DeleteTaskSession(ctx, sessionID)
		}()
		close(start)
		wait.Wait()

		status := queue.GetStatus(ctx, sessionID)
		_, sessionErr := repo.GetTaskSession(ctx, sessionID)
		switch {
		case queueErr == nil:
			if deleteErr == nil || sessionErr != nil || status.Count != 1 || status.Entries[0].Content != "exact race body" {
				t.Fatalf("iteration %d admitted outcome: queueErr=%v deleteErr=%v sessionErr=%v status=%+v", iteration, queueErr, deleteErr, sessionErr, status)
			}
		case deleteErr == nil:
			if sessionErr == nil || status.Count != 0 {
				t.Fatalf("iteration %d deleted outcome: queueErr=%v sessionErr=%v status=%+v", iteration, queueErr, sessionErr, status)
			}
		default:
			t.Fatalf("iteration %d neither operation committed: queueErr=%v deleteErr=%v", iteration, queueErr, deleteErr)
		}
	}
}

func TestQueueClaimAndSessionDeleteRaceReturnsOrPreservesExactHead(t *testing.T) {
	repo := newRepoForArchiveTests(t, "task-session-delete-claim-race")
	ctx := context.Background()
	seedLiveSessionForQueue(t, repo, "session-claim-race", "task-session-delete-claim-race")
	mqRepo, err := messagequeue.NewSQLiteRepository(repo.db, repo.db)
	if err != nil {
		t.Fatalf("NewSQLiteRepository: %v", err)
	}
	log, err := logger.NewLogger(logger.LoggingConfig{Level: "error", Format: "console"})
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	queue := messagequeue.NewService(mqRepo, messagequeue.DefaultMaxPerSession, log)
	queue.SetAutoMergeEnabled(false)
	queued, err := queue.QueueMessage(ctx, "session-claim-race", "task-session-delete-claim-race", "claimed exact body", "", "user", false, nil)
	if err != nil {
		t.Fatalf("QueueMessage: %v", err)
	}

	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	var claimed *messagequeue.QueuedMessage
	var claimErr, deleteErr error
	go func() {
		defer wait.Done()
		<-start
		claimed, claimErr = mqRepo.ReserveHead(ctx, "session-claim-race")
	}()
	go func() {
		defer wait.Done()
		<-start
		deleteErr = repo.DeleteTaskSession(ctx, "session-claim-race")
	}()
	close(start)
	wait.Wait()
	if claimErr != nil || claimed == nil || claimed.ID != queued.ID || claimed.Content != "claimed exact body" {
		t.Fatalf("claim readback = %+v err=%v, want exact queued head", claimed, claimErr)
	}
	if deleteErr != nil {
		if _, err := repo.GetTaskSession(ctx, "session-claim-race"); err != nil {
			t.Fatalf("rejected delete did not preserve session: %v", err)
		}
	}
	if status := queue.GetStatus(ctx, "session-claim-race"); status.Count != 0 {
		t.Fatalf("queue after exact claim = %+v, want empty", status)
	}
}

func TestQueueRecoveryAndCleanupReceiptSurviveRestart(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "session-cleanup-restart.db")
	openRepos := func() (*Repository, *messagequeue.Service, *sqlx.DB) {
		raw, err := taskdb.OpenSQLite(dbPath)
		if err != nil {
			t.Fatalf("OpenSQLite: %v", err)
		}
		database := sqlx.NewDb(raw, "sqlite3")
		repo, err := NewWithDB(database, database, nil)
		if err != nil {
			t.Fatalf("NewWithDB: %v", err)
		}
		queueRepo, err := messagequeue.NewSQLiteRepository(database, database)
		if err != nil {
			t.Fatalf("NewSQLiteRepository: %v", err)
		}
		log, err := logger.NewLogger(logger.LoggingConfig{Level: "error", Format: "console"})
		if err != nil {
			t.Fatalf("logger: %v", err)
		}
		queue := messagequeue.NewService(queueRepo, messagequeue.DefaultMaxPerSession, log)
		queue.SetAutoMergeEnabled(false)
		return repo, queue, database
	}

	repo, queue, database := openRepos()
	if err := repo.CreateTask(ctx, &models.Task{ID: "task-restart", WorkspaceID: "workspace-restart", Title: "Restart"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	seedLiveSessionForQueue(t, repo, "session-queued", "task-restart")
	seedLiveSessionForQueue(t, repo, "session-disposable", "task-restart")
	if _, err := queue.QueueMessage(ctx, "session-queued", "task-restart", "first after restart", "model-a", "peer", false, nil); err != nil {
		t.Fatalf("QueueMessage first: %v", err)
	}
	if _, err := queue.QueueMessage(ctx, "session-queued", "task-restart", "second after restart", "model-b", "human", true, nil); err != nil {
		t.Fatalf("QueueMessage second: %v", err)
	}
	if err := repo.DeleteTaskSession(ctx, "session-disposable"); err != nil {
		t.Fatalf("DeleteTaskSession: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close before restart: %v", err)
	}

	restartedRepo, restartedQueue, restartedDB := openRepos()
	defer func() { _ = restartedDB.Close() }()
	entries, err := restartedQueue.RecoverySnapshot(ctx, "session-queued")
	if err != nil {
		t.Fatalf("RecoverySnapshot: %v", err)
	}
	if len(entries) != 2 || entries[0].Content != "first after restart" || entries[1].Content != "second after restart" ||
		entries[0].ContentSHA256 != "0ab4d9c6b98c60f973e7ec1dd48870d658b9fa04546e8e3950dfff0df80cf254" ||
		entries[1].ContentSHA256 != "46b71a5a92be84337305fa31d5cd660e37f6217f4f1ef2cdca93fb9e5911c6c4" {
		t.Fatalf("restart FIFO readback = %+v", entries)
	}
	receipt, err := restartedRepo.GetTaskSessionCleanupReceipt(ctx, "task-restart", "session-disposable")
	if err != nil {
		t.Fatalf("cleanup receipt after restart: %v", err)
	}
	if receipt.WorkspaceID != "workspace-restart" || receipt.Disposition != "deleted" {
		t.Fatalf("restart receipt = %+v", receipt)
	}
}

func seedLiveSessionForQueue(t *testing.T, repo *Repository, sessionID, taskID string) {
	t.Helper()
	now := time.Now().UTC()
	if err := repo.CreateTaskSession(context.Background(), &models.TaskSession{
		ID: sessionID, TaskID: taskID,
		State: models.TaskSessionStateCompleted, StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateTaskSession(%s): %v", sessionID, err)
	}
}
