package service

import (
	"context"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	officesqlite "github.com/kandev/kandev/internal/office/repository/sqlite"
	"github.com/kandev/kandev/internal/task/models"
)

func TestSequentialHandoffBlockerUsesAuditedMutation(t *testing.T) {
	svc, repo := setupOfficeTest(t)
	database := sqlx.NewDb(repo.DB(), "sqlite3")
	office, err := officesqlite.NewWithDB(database, database, nil)
	if err != nil {
		t.Fatalf("office repository: %v", err)
	}
	ctx := context.Background()
	parent := mustSeedTask(t, svc, "Parent")
	now := time.Now().UTC()
	for index, id := range []string{"first-child", "second-child"} {
		created := now.Add(time.Duration(index) * time.Second)
		if err := repo.CreateTask(ctx, &models.Task{
			ID: id, WorkspaceID: parent.WorkspaceID, WorkflowID: parent.WorkflowID,
			WorkflowStepID: parent.WorkflowStepID, ParentID: parent.ID,
			Title: id, Priority: models.TaskPriorityMedium,
			CreatedAt: created, UpdatedAt: created,
		}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	handoff := NewHandoffService(repo, nil, nil, office, nil, nil)
	if err := handoff.attachSequentialBlocker(ctx, "second-child", parent.ID); err != nil {
		t.Fatalf("attach sequential blocker: %v", err)
	}
	var edge, revised, audited int
	if err := database.GetContext(ctx, &edge,
		`SELECT COUNT(*) FROM task_blockers WHERE task_id = 'second-child' AND blocker_task_id = 'first-child'`); err != nil {
		t.Fatalf("read edge: %v", err)
	}
	if err := database.GetContext(ctx, &revised,
		`SELECT COUNT(*) FROM tasks WHERE id IN ('first-child', 'second-child') AND dependency_revision = 1`); err != nil {
		t.Fatalf("read revisions: %v", err)
	}
	if err := database.GetContext(ctx, &audited,
		`SELECT COUNT(*) FROM task_dependency_mutation_audit WHERE origin_class = 'handoff' AND changed = 1`); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if edge != 1 || revised != 2 || audited != 1 {
		t.Fatalf("edge %d, revised endpoints %d, audits %d; want 1, 2, 1", edge, revised, audited)
	}
}
