package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"

	dbutil "github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/task/models"
)

func newRetentionSQLitePair(t *testing.T) (*Repository, *Repository) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "retention-race.db")
	open := func() *Repository {
		raw, err := dbutil.OpenSQLite(path)
		if err != nil {
			t.Fatal(err)
		}
		db := sqlx.NewDb(raw, "sqlite3")
		t.Cleanup(func() { _ = db.Close() })
		repo, err := NewWithDB(db, db, nil)
		if err != nil {
			t.Fatal(err)
		}
		return repo
	}
	return open(), open()
}

func exerciseRetentionWinnerOrders(t *testing.T, first, second *Repository) {
	t.Helper()
	ctx := context.Background()
	seedBarrierTask(t, first, "retention-parent")
	for _, id := range []string{"hold-first", "cleanup-first", "rolled-back"} {
		seedBarrierTask(t, first, id)
		if _, err := first.db.ExecContext(ctx, first.db.Rebind(`UPDATE tasks SET parent_id = ? WHERE id = ?`),
			"retention-parent", id); err != nil {
			t.Fatal(err)
		}
	}
	hold, err := first.GetTask(ctx, "hold-first")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.UpdateTaskWithParentRetention(ctx, hold, "retention-parent", "ws-1", true, false); err != nil {
		t.Fatal(err)
	}
	if err := second.CreateTaskResourceCleanupJob(ctx, &models.TaskResourceCleanupJob{
		OperationID: "hold-first-op", TaskID: "hold-first", Trigger: models.TaskResourceCleanupTriggerArchive,
		State: models.TaskResourceCleanupStatePrepared,
	}); err == nil {
		t.Fatal("cleanup overtook committed hold on second connection")
	}
	if err := first.CreateTaskResourceCleanupJob(ctx, &models.TaskResourceCleanupJob{
		OperationID: "cleanup-first-op", TaskID: "cleanup-first", Trigger: models.TaskResourceCleanupTriggerArchive,
		State: models.TaskResourceCleanupStatePrepared,
	}); err != nil {
		t.Fatal(err)
	}
	clean, err := second.GetTask(ctx, "cleanup-first")
	if err != nil {
		t.Fatal(err)
	}
	if err := second.UpdateTaskWithParentRetention(ctx, clean, "retention-parent", "ws-1", true, false); err == nil {
		t.Fatal("hold overtook prepared cleanup on second connection")
	}
	got, err := first.GetTask(ctx, "cleanup-first")
	if err != nil || models.IsTerminalRetentionHeld(got.Metadata) {
		t.Fatalf("cleanup winner gained a hold: %+v, %v", got, err)
	}
	tx, err := first.db.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, first.db.Rebind(`UPDATE tasks SET metadata = ? WHERE id = ?`),
		`{"terminal_retention":true}`, "rolled-back"); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := second.CreateTaskResourceCleanupJob(ctx, &models.TaskResourceCleanupJob{
		OperationID: "rolled-back-op", TaskID: "rolled-back", Trigger: models.TaskResourceCleanupTriggerArchive,
		State: models.TaskResourceCleanupStatePrepared,
	}); err != nil {
		t.Fatalf("rolled-back hold blocked cleanup: %v", err)
	}
}

func TestRetentionAdmission_TwoSQLiteConnectionsWinnerOrders(t *testing.T) {
	first, second := newRetentionSQLitePair(t)
	exerciseRetentionWinnerOrders(t, first, second)
	ctx := context.Background()
	job, err := second.GetTaskResourceCleanupJobByOperationID(ctx, "cleanup-first-op")
	if err != nil || job.State != models.TaskResourceCleanupStatePrepared {
		t.Fatalf("prepared job lost on second connection: %+v, %v", job, err)
	}
}

func TestRetentionAdmission_TwoPostgresConnectionsWinnerOrders(t *testing.T) {
	first, second, _ := newTaskPostgresRepoPair(t)
	exerciseRetentionWinnerOrders(t, first, second)
}

func TestCleanupEnvironmentTransferRequiresWinningReservationAcrossConnections(t *testing.T) {
	first, second := newRetentionSQLitePair(t)
	ctx := context.Background()
	seedBarrierTask(t, first, "transfer-parent")
	for _, id := range []string{"transfer-held", "transfer-reserved", "transfer-borrower", "transfer-rolled", "transfer-rolled-borrower"} {
		seedBarrierTask(t, first, id)
	}
	for _, id := range []string{"transfer-held", "transfer-reserved", "transfer-rolled"} {
		if _, err := first.db.ExecContext(ctx, first.db.Rebind(`UPDATE tasks SET parent_id = ? WHERE id = ?`), "transfer-parent", id); err != nil {
			t.Fatal(err)
		}
		if err := first.CreateTaskEnvironment(ctx, &models.TaskEnvironment{
			ID: "env-" + id, TaskID: id, ExecutorType: string(models.ExecutorTypeLocal),
			Status: models.TaskEnvironmentStatusReady,
		}); err != nil {
			t.Fatal(err)
		}
	}
	held, err := first.GetTask(ctx, "transfer-held")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.UpdateTaskWithParentRetention(ctx, held, "transfer-parent", "ws-1", true, false); err != nil {
		t.Fatal(err)
	}
	if err := second.CreateTaskResourceCleanupJob(ctx, &models.TaskResourceCleanupJob{
		OperationID: "transfer-held-op", TaskID: "transfer-held", Trigger: models.TaskResourceCleanupTriggerDelete,
		State: models.TaskResourceCleanupStatePrepared,
	}); err == nil {
		t.Fatal("held task gained cleanup reservation")
	}
	if err := second.TransferTaskEnvironmentOwnershipForCleanup(ctx, "env-transfer-held", "transfer-held", 1, "transfer-borrower", "transfer-held-op"); err == nil {
		t.Fatal("held task transferred environment without reservation")
	}
	heldEnv, err := first.GetTaskEnvironment(ctx, "env-transfer-held")
	if err != nil || heldEnv.TaskID != "transfer-held" || heldEnv.OwnershipGeneration != 1 {
		t.Fatalf("losing cleanup changed held environment: %+v, %v", heldEnv, err)
	}
	if err := first.CreateTaskResourceCleanupJob(ctx, &models.TaskResourceCleanupJob{
		OperationID: "transfer-reserved-op", TaskID: "transfer-reserved", Trigger: models.TaskResourceCleanupTriggerDelete,
		State: models.TaskResourceCleanupStatePrepared,
	}); err != nil {
		t.Fatal(err)
	}
	reserved, err := second.GetTask(ctx, "transfer-reserved")
	if err != nil {
		t.Fatal(err)
	}
	if err := second.UpdateTaskWithParentRetention(ctx, reserved, "transfer-parent", "ws-1", true, false); err == nil {
		t.Fatal("hold overtook prepared transfer reservation")
	}
	if err := second.TransferTaskEnvironmentOwnershipForCleanup(ctx, "env-transfer-reserved", "transfer-reserved", 1, "transfer-borrower", "wrong-op"); err == nil {
		t.Fatal("transfer accepted unrelated operation")
	}
	if err := second.TransferTaskEnvironmentOwnershipForCleanup(ctx, "env-transfer-reserved", "transfer-reserved", 1, "transfer-borrower", "transfer-reserved-op"); err != nil {
		t.Fatal(err)
	}
	if err := second.TransferTaskEnvironmentOwnershipForCleanup(ctx, "env-transfer-reserved", "transfer-reserved", 1, "transfer-borrower", "transfer-reserved-op"); err == nil {
		t.Fatal("stale transfer replay changed the current generation")
	}
	got, err := first.GetTaskEnvironment(ctx, "env-transfer-reserved")
	if err != nil || got.TaskID != "transfer-borrower" || got.OwnershipGeneration != 2 {
		t.Fatalf("reserved transfer not durable across connections: %+v, %v", got, err)
	}
	tx, err := first.db.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, first.db.Rebind(`UPDATE tasks SET metadata = ? WHERE id = ?`),
		`{"terminal_retention":true}`, "transfer-rolled"); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := second.CreateTaskResourceCleanupJob(ctx, &models.TaskResourceCleanupJob{
		OperationID: "transfer-rolled-op", TaskID: "transfer-rolled", Trigger: models.TaskResourceCleanupTriggerDelete,
		State: models.TaskResourceCleanupStatePrepared,
	}); err != nil {
		t.Fatalf("rolled-back hold blocked reservation: %v", err)
	}
	if err := first.TransferTaskEnvironmentOwnershipForCleanup(ctx, "env-transfer-rolled", "transfer-rolled", 1, "transfer-rolled-borrower", "transfer-rolled-op"); err != nil {
		t.Fatalf("rolled-back hold blocked transfer: %v", err)
	}
	var databaseIndex int
	var databaseName, databasePath string
	if err := first.db.QueryRowContext(ctx, `PRAGMA database_list`).Scan(&databaseIndex, &databaseName, &databasePath); err != nil {
		t.Fatal(err)
	}
	reopenedDB, err := dbutil.OpenSQLite(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopenedDB.Close() })
	reopenedSQL := sqlx.NewDb(reopenedDB, "sqlite3")
	reopened, err := NewWithDB(reopenedSQL, reopenedSQL, nil)
	if err != nil {
		t.Fatal(err)
	}
	reopenedEnv, err := reopened.GetTaskEnvironment(ctx, "env-transfer-rolled")
	if err != nil || reopenedEnv.TaskID != "transfer-rolled-borrower" || reopenedEnv.OwnershipGeneration != 2 {
		t.Fatalf("transfer lost after reopened connection: %+v, %v", reopenedEnv, err)
	}
}

func TestCleanupEnvironmentTransfer_TwoPostgresConnections(t *testing.T) {
	first, second, _ := newTaskPostgresRepoPair(t)
	ctx := context.Background()
	seedBarrierTask(t, first, "pg-transfer-parent")
	seedBarrierTask(t, first, "pg-transfer-owner")
	seedBarrierTask(t, first, "pg-transfer-borrower")
	if _, err := first.db.ExecContext(ctx, first.db.Rebind(`UPDATE tasks SET parent_id = ? WHERE id = ?`),
		"pg-transfer-parent", "pg-transfer-owner"); err != nil {
		t.Fatal(err)
	}
	if err := first.CreateTaskEnvironment(ctx, &models.TaskEnvironment{
		ID: "pg-transfer-env", TaskID: "pg-transfer-owner", ExecutorType: string(models.ExecutorTypeLocal),
		Status: models.TaskEnvironmentStatusReady,
	}); err != nil {
		t.Fatal(err)
	}
	if err := first.CreateTaskResourceCleanupJob(ctx, &models.TaskResourceCleanupJob{
		OperationID: "pg-transfer-op", TaskID: "pg-transfer-owner", Trigger: models.TaskResourceCleanupTriggerDelete,
		State: models.TaskResourceCleanupStatePrepared,
	}); err != nil {
		t.Fatal(err)
	}
	owner, err := second.GetTask(ctx, "pg-transfer-owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := second.UpdateTaskWithParentRetention(ctx, owner, "pg-transfer-parent", "ws-1", true, false); err == nil {
		t.Fatal("Postgres hold overtook prepared cleanup")
	}
	if err := second.TransferTaskEnvironmentOwnershipForCleanup(ctx, "pg-transfer-env", "pg-transfer-owner", 1,
		"pg-transfer-borrower", "pg-transfer-op"); err != nil {
		t.Fatal(err)
	}
	env, err := first.GetTaskEnvironment(ctx, "pg-transfer-env")
	if err != nil || env.TaskID != "pg-transfer-borrower" || env.OwnershipGeneration != 2 {
		t.Fatalf("Postgres reserved transfer not visible on first connection: %+v, %v", env, err)
	}
}
