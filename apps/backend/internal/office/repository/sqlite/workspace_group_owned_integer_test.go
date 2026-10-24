package sqlite_test

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/office/models"
)

func TestWorkspaceGroupOwnedIntegerContract(t *testing.T) {
	ctx := context.Background()
	repo := newWorkspaceGroupTestRepo(t)
	insertWGTask(t, repo, "archived-member", true)
	group := &models.WorkspaceGroup{ID: "owned-integer", WorkspaceID: "ws-1",
		OwnerTaskID: "archived-member", MaterializedKind: models.WorkspaceGroupKindPlainFolder}
	if err := repo.CreateWorkspaceGroup(ctx, group); err != nil {
		t.Fatal(err)
	}
	rawOwned := func() int {
		t.Helper()
		var value int
		if err := repo.ReaderDB().GetContext(ctx, &value,
			`SELECT owned_by_kandev FROM task_workspace_groups WHERE id = ?`, group.ID); err != nil {
			t.Fatal(err)
		}
		return value
	}
	if got := rawOwned(); got != 0 {
		t.Fatalf("new user-owned group raw ownership = %d, want 0", got)
	}
	directOwned := &models.WorkspaceGroup{ID: "direct-owned-integer", WorkspaceID: "ws-1",
		OwnerTaskID: "archived-member", MaterializedKind: models.WorkspaceGroupKindPlainFolder,
		OwnedByKandev: true, CleanupPolicy: models.WorkspaceCleanupPolicyDeleteWhenLastMemberArchivedOrDel}
	if err := repo.CreateWorkspaceGroup(ctx, directOwned); err != nil {
		t.Fatal(err)
	}
	var directlyStored int
	if err := repo.ReaderDB().GetContext(ctx, &directlyStored,
		`SELECT owned_by_kandev FROM task_workspace_groups WHERE id = ?`, directOwned.ID); err != nil {
		t.Fatal(err)
	}
	if directlyStored != 1 {
		t.Fatalf("new owned group raw ownership = %d, want 1", directlyStored)
	}
	if claimed, err := repo.ClaimWorkspaceGroupCleanup(ctx, group.ID, group.OwnershipGeneration); err != nil || claimed {
		t.Fatalf("user-owned group claim = %v, %v; want denied", claimed, err)
	}
	if err := repo.MarkWorkspaceMaterialized(ctx, group.ID, models.MaterializedWorkspace{
		Path: "/synthetic/owned-integer", Kind: models.WorkspaceGroupKindPlainFolder, OwnedByKandev: true,
	}); err != nil {
		t.Fatal(err)
	}
	if got := rawOwned(); got != 1 {
		t.Fatalf("materialized owned group raw ownership = %d, want 1", got)
	}
	if err := repo.AddWorkspaceGroupMember(ctx, group.ID, "archived-member", ""); err != nil {
		t.Fatal(err)
	}
	if deferred, err := repo.DeferWorkspaceGroupCleanup(ctx, group.ID, group.OwnershipGeneration); err != nil || !deferred {
		t.Fatalf("defer owned group = %v, %v", deferred, err)
	}
	listed, err := repo.ListDeferredWorkspaceGroupCleanups(ctx, "", 10)
	if err != nil || len(listed) != 1 || listed[0].ID != group.ID {
		t.Fatalf("deferred owned group list = %+v, %v", listed, err)
	}
	if claimed, err := repo.ClaimWorkspaceGroupCleanup(ctx, group.ID, group.OwnershipGeneration); err != nil || !claimed {
		t.Fatalf("owned group claim = %v, %v; want true", claimed, err)
	}
	if got := rawOwned(); got != 1 {
		t.Fatalf("claimed group raw ownership = %d, want 1", got)
	}
}
