package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/queuepurge"
	wfmodels "github.com/kandev/kandev/internal/workflow/models"
)

func TestCascadeMutationDriverCommitOutcomesRetainExactOperation(t *testing.T) {
	for _, mutation := range []string{"archive", "delete", "autoarchive"} {
		for _, mode := range []string{"committed-error", "rollback-error"} {
			t.Run(mutation+"/"+mode, func(t *testing.T) {
				ctx := context.Background()
				fault := &commitFault{}
				name := fmt.Sprintf("cascade-commit-%d", commitDriverSequence.Add(1))
				sql.Register(name, commitDriver{fault})
				raw, err := sql.Open(name, "file:"+filepath.Join(t.TempDir(), "cascade.db")+"?_foreign_keys=on&_busy_timeout=5000&_journal_mode=WAL")
				if err != nil {
					t.Fatal(err)
				}
				raw.SetMaxOpenConns(1)
				t.Cleanup(func() { _ = raw.Close() })
				db := sqlx.NewDb(raw, "sqlite3")
				repo, err := NewWithDB(db, db, nil)
				if err != nil {
					t.Fatal(err)
				}
				seedWorkspace(t, repo, "ws-cascade")
				oldUpdatedAt := time.Now().UTC().Add(-48 * time.Hour)
				if mutation == "autoarchive" {
					if err := repo.CreateWorkflow(ctx, &models.Workflow{ID: "wf-cascade", WorkspaceID: "ws-cascade", Name: "Cascade"}); err != nil {
						t.Fatal(err)
					}
					steps := newAutoOriginWorkflowStore(t, repo)
					if err := steps.CreateStep(ctx, &wfmodels.WorkflowStep{ID: "step-cascade", WorkflowID: "wf-cascade", Name: "Done", AutoArchiveAfterHours: 1}); err != nil {
						t.Fatal(err)
					}
				}
				task := &models.Task{ID: "task-cascade", WorkspaceID: "ws-cascade", Title: "Cascade"}
				if mutation == "autoarchive" {
					task.WorkflowID, task.WorkflowStepID = "wf-cascade", "step-cascade"
				}
				if err := repo.CreateTask(ctx, task); err != nil {
					t.Fatal(err)
				}
				if mutation == "autoarchive" {
					if _, err := db.ExecContext(ctx, `UPDATE tasks SET updated_at = ? WHERE id = ?`, oldUpdatedAt, task.ID); err != nil {
						t.Fatal(err)
					}
				}
				trigger := mutation
				if mutation == "autoarchive" {
					trigger = "archive"
				}
				operationID := "cascade_" + trigger + ":cascade-1:" + task.ID
				mutationCtx := queuepurge.WithTaskQueuePurgeOperations(ctx, map[string]string{task.ID: operationID})
				fault.Lock()
				fault.mode = mode
				fault.Unlock()
				switch mutation {
				case "archive":
					_, _, err = repo.ArchiveTaskIfActiveWithVacatedStep(mutationCtx, task.ID, "cascade-1")
				case "autoarchive":
					_, err = repo.ArchiveTaskIfAutoArchiveEligible(mutationCtx, task.ID, oldUpdatedAt, "cascade-1")
				case "delete":
					_, err = repo.DeleteTaskWithVacatedStep(mutationCtx, task.ID)
				}
				if !errors.Is(err, errCommitReceiptLost) {
					t.Fatalf("mutation error = %v, want injected driver commit error", err)
				}
				current, readErr := repo.GetTask(ctx, task.ID)
				committed := mode == "committed-error"
				if mutation == "delete" {
					if (current == nil) != committed {
						t.Fatalf("delete committed=%v task=%+v read=%v", committed, current, readErr)
					}
				} else if readErr != nil || current == nil || (current.ArchivedAt != nil) != committed {
					t.Fatalf("archive committed=%v task=%+v read=%v", committed, current, readErr)
				}
				identity := taskQueuePurgeIdentity{taskID: task.ID, operationID: operationID}
				if repo.queuePurgeUnknown[identity.key()] != 1 {
					t.Fatalf("exact operation unknown count = %d", repo.queuePurgeUnknown[identity.key()])
				}
			})
		}
	}
}
