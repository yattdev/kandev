package sqlite

import (
	"encoding/json"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
)

// @covers AC-COORDINATOR-DEPENDENCY-002.1 AC-COORDINATOR-DEPENDENCY-002.2
func TestDependencyMutationSchemaHasRevisionAndAudit(t *testing.T) {
	repo := newRepoForSessionTests(t)
	if err := repo.CreateTask(t.Context(), &models.Task{ID: "dependency-schema-task", Title: "dependency"}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	var revision int64
	if err := repo.db.GetContext(t.Context(), &revision,
		`SELECT dependency_revision FROM tasks WHERE id = ?`, "dependency-schema-task"); err != nil {
		t.Fatalf("read dependency revision: %v", err)
	}
	if revision != 0 {
		t.Fatalf("dependency revision = %d, want 0", revision)
	}
	var auditTable string
	if err := repo.db.GetContext(t.Context(), &auditTable,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'task_dependency_mutation_audit'`); err != nil {
		t.Fatalf("read dependency audit table: %v", err)
	}
	if auditTable != "task_dependency_mutation_audit" {
		t.Fatalf("audit table = %q", auditTable)
	}
}

// @covers AC-COORDINATOR-DEPENDENCY-002.2
func TestTaskReadProjectsDependencyRevision(t *testing.T) {
	repo := newRepoForSessionTests(t)
	if err := repo.CreateTask(t.Context(), &models.Task{ID: "dependency-revision-task", Title: "dependency"}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := repo.db.ExecContext(t.Context(),
		`UPDATE tasks SET dependency_revision = 7 WHERE id = ?`, "dependency-revision-task"); err != nil {
		t.Fatalf("set dependency revision: %v", err)
	}
	task, err := repo.GetTask(t.Context(), "dependency-revision-task")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	encoded, err := json.Marshal(task.ToAPI())
	if err != nil {
		t.Fatalf("encode task: %v", err)
	}
	var projected map[string]any
	if err := json.Unmarshal(encoded, &projected); err != nil {
		t.Fatalf("decode task: %v", err)
	}
	if projected["dependency_revision"] != float64(7) {
		t.Fatalf("projected dependency revision = %v, want 7", projected["dependency_revision"])
	}
}
