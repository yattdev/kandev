package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/kandev/kandev/internal/office/models"
)

func TestWorkspaceGroupHistoricalDeletedTaskClaimAndAdmission(t *testing.T) {
	repo := newWorkspaceGroupTestRepo(t)
	ctx := context.Background()
	insertWGTask(t, repo, "historical", false)
	group := &models.WorkspaceGroup{ID: "historical-group", WorkspaceID: "ws-1", OwnerTaskID: "historical",
		MaterializedKind: models.WorkspaceGroupKindPlainFolder, OwnedByKandev: true,
		CleanupPolicy: models.WorkspaceCleanupPolicyDeleteWhenLastMemberArchivedOrDel}
	if err := repo.CreateWorkspaceGroup(ctx, group); err != nil {
		t.Fatal(err)
	}
	if err := repo.AddWorkspaceGroupMember(ctx, group.ID, "historical", ""); err != nil {
		t.Fatal(err)
	}
	if claimed, err := repo.ClaimWorkspaceGroupCleanup(ctx, group.ID, group.OwnershipGeneration); err != nil || claimed {
		t.Fatalf("live member claim = %v, %v; want denied", claimed, err)
	}
	if _, err := repo.ExecRaw(ctx, `DELETE FROM tasks WHERE id = ?`, "historical"); err != nil {
		t.Fatal(err)
	}
	if err := repo.AddWorkspaceGroupMember(ctx, group.ID, "historical", ""); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing task membership = %v, want sql.ErrNoRows", err)
	}
	if err := repo.AddWorkspaceGroupMember(ctx, group.ID, "never-existed", ""); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("never-existing task membership = %v, want sql.ErrNoRows", err)
	}
	// A legacy orphan executor still blocks cleanup. Production admission
	// now denies creating one, but a preexisting row cannot be ignored.
	if _, err := repo.ExecRaw(ctx, `INSERT INTO executors_running(id, session_id, task_id) VALUES (?, ?, ?)`,
		"legacy-running", "legacy-session", "historical"); err != nil {
		t.Fatal(err)
	}
	if claimed, err := repo.ClaimWorkspaceGroupCleanup(ctx, group.ID, group.OwnershipGeneration); err != nil || claimed {
		t.Fatalf("legacy orphan executor claim = %v, %v; want denied", claimed, err)
	}
	if _, err := repo.ExecRaw(ctx, `DELETE FROM executors_running WHERE id = ?`, "legacy-running"); err != nil {
		t.Fatal(err)
	}
	// Reappearing under the historical ID restores the live-member veto.
	insertWGTask(t, repo, "historical", false)
	if claimed, err := repo.ClaimWorkspaceGroupCleanup(ctx, group.ID, group.OwnershipGeneration); err != nil || claimed {
		t.Fatalf("reappeared live task claim = %v, %v; want denied", claimed, err)
	}
	if _, err := repo.ExecRaw(ctx, `DELETE FROM tasks WHERE id = ?`, "historical"); err != nil {
		t.Fatal(err)
	}
	if claimed, err := repo.ClaimWorkspaceGroupCleanup(ctx, group.ID, group.OwnershipGeneration); err != nil || !claimed {
		t.Fatalf("historical deleted task claim = %v, %v; want true", claimed, err)
	}
}
