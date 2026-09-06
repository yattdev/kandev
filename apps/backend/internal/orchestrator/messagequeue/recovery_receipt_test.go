package messagequeue

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
)

func TestSQLiteQueueRecoveryReceiptSurvivesRestartWithIdentityHashParity(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "queue-recovery.db")
	firstDB, firstRepo := openRecoveryReceiptRepository(t, dbPath)
	ctx := context.Background()
	for _, content := range []string{"first exact body", "second exact body"} {
		if err := firstRepo.Insert(ctx, &QueuedMessage{
			SessionID: "source", TaskID: "task-1", Content: content, QueuedBy: QueuedByUser,
		}, 10); err != nil {
			t.Fatalf("insert %q: %v", content, err)
		}
	}
	committed := recoverQueueWithReceipt(t, firstRepo, QueueRecoveryScope{
		TaskID: "task-1", WorkspaceID: "workspace-1",
		SourceSessionID: "source", DestinationSessionID: "destination",
	})
	if err := firstDB.Close(); err != nil {
		t.Fatalf("close first database: %v", err)
	}

	secondDB, secondRepo := openRecoveryReceiptRepository(t, dbPath)
	t.Cleanup(func() { _ = secondDB.Close() })
	replayed := recoverQueueWithReceipt(t, secondRepo, QueueRecoveryScope{
		TaskID: "task-1", WorkspaceID: "workspace-1",
		SourceSessionID: "source", DestinationSessionID: "destination",
	})
	if replayed.Receipt.ID != committed.Receipt.ID ||
		replayed.Receipt.SnapshotSHA256 != committed.Receipt.SnapshotSHA256 {
		t.Fatalf("replayed receipt = %#v, want identity/hash from %#v", replayed.Receipt, committed.Receipt)
	}
	if len(replayed.Entries) != 2 || replayed.Entries[0].ID != committed.Entries[0].ID ||
		replayed.Entries[1].ID != committed.Entries[1].ID ||
		replayed.Entries[0].Content != "first exact body" || replayed.Entries[1].Content != "second exact body" {
		t.Fatalf("replayed FIFO snapshot = %#v, want %#v", replayed.Entries, committed.Entries)
	}
}

func TestSQLiteQueueRecoveryReceiptRejectsCorruptedSnapshot(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "queue-recovery-corrupt.db")
	db, repo := openRecoveryReceiptRepository(t, dbPath)
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := repo.Insert(ctx, &QueuedMessage{
		SessionID: "source", TaskID: "task-1", Content: "exact body", QueuedBy: QueuedByUser,
	}, 10); err != nil {
		t.Fatalf("insert: %v", err)
	}
	scope := QueueRecoveryScope{
		TaskID: "task-1", WorkspaceID: "workspace-1",
		SourceSessionID: "source", DestinationSessionID: "destination",
	}
	recoverQueueWithReceipt(t, repo, scope)
	if _, err := db.Exec(`UPDATE queue_recovery_receipts SET snapshot_json = '[]' WHERE source_session_id = 'source'`); err != nil {
		t.Fatalf("corrupt receipt snapshot: %v", err)
	}

	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatalf("begin replay: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = RecoverSessionQueueInTransaction(ctx, tx, db, scope)
	if err == nil || errors.Is(err, ErrQueueRecoveryConflict) {
		t.Fatalf("corrupt replay error = %v, want integrity failure", err)
	}
}

func openRecoveryReceiptRepository(t *testing.T, path string) (*sqlx.DB, *sqliteRepository) {
	t.Helper()
	raw, err := sql.Open("sqlite3", path+"?_foreign_keys=on")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	raw.SetMaxOpenConns(1)
	raw.SetMaxIdleConns(1)
	db := sqlx.NewDb(raw, "sqlite3")
	repository, err := NewSQLiteRepository(db, db)
	if err != nil {
		_ = db.Close()
		t.Fatalf("NewSQLiteRepository: %v", err)
	}
	return db, repository.(*sqliteRepository)
}

func recoverQueueWithReceipt(
	t *testing.T,
	repo *sqliteRepository,
	scope QueueRecoveryScope,
) *QueueRecoveryResult {
	t.Helper()
	tx, err := repo.db.BeginTxx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin recovery: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, err := RecoverSessionQueueInTransaction(context.Background(), tx, repo.db, scope)
	if err != nil {
		t.Fatalf("RecoverSessionQueueInTransaction: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit recovery: %v", err)
	}
	return result
}
