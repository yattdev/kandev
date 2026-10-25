package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/hierarchy"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
	v1 "github.com/kandev/kandev/pkg/api/v1"
)

func TestScopedTerminalRetentionRejectsFormerParentWithoutApplyingFields(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForEntityTests(t)
	if err := repo.CreateWorkspace(ctx, &models.Workspace{ID: "retention-ws", Name: "Retention"}); err != nil {
		t.Fatal(err)
	}
	for _, task := range []*models.Task{
		{ID: "old-parent", WorkspaceID: "retention-ws", Title: "Old"},
		{ID: "new-parent", WorkspaceID: "retention-ws", Title: "New"},
		{ID: "child", WorkspaceID: "retention-ws", ParentID: "old-parent", Title: "Child"},
	} {
		if err := repo.CreateTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	newParent := "new-parent"
	if _, err := repo.UpdateTaskFieldsWithParentAdmission(ctx, "child", models.TaskFieldUpdate{ParentID: &newParent}, hierarchy.ValidateParent); err != nil {
		t.Fatal(err)
	}
	title, completed := "Unauthorized title", v1.TaskStateCompleted
	_, err := repo.UpdateTaskFieldsWithParentAdmission(ctx, "child", models.TaskFieldUpdate{
		Title: &title, State: &completed,
		TerminalRetentionScope: &models.TerminalRetentionScope{
			ParentID: "old-parent", WorkspaceID: "retention-ws", Held: true,
		},
	}, hierarchy.ValidateParent)
	if !errors.Is(err, repoerrors.ErrTaskParentMismatch) {
		t.Fatalf("update error = %v, want parent mismatch", err)
	}
	child, err := repo.GetTask(ctx, "child")
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentID != newParent || child.Title != "Child" || child.State == completed || models.IsTerminalRetentionHeld(child.Metadata) {
		t.Fatalf("rejected update changed child: %+v", child)
	}
}
