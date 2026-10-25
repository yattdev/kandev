package sqlite_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/task/dependencywrite"
	"github.com/kandev/kandev/internal/task/models"
)

// @covers AC-COORDINATOR-DEPENDENCY-002.1 AC-COORDINATOR-DEPENDENCY-002.2
func TestDependencyMutationCommitsEdgeRevisionsAndAudit(t *testing.T) {
	repo, _, database := newManagedDeletionSQLitePair(t)
	ctx := t.Context()
	if err := repo.CreateWorkspace(ctx, &models.Workspace{ID: "dependency-ws", Name: "Dependencies"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	for _, id := range []string{"dependent", "predecessor"} {
		if err := repo.CreateTask(ctx, &models.Task{ID: id, WorkspaceID: "dependency-ws", Title: id}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	writer, ok := any(repo).(interface {
		MutateDependency(context.Context, dependencywrite.DependencyMutationRequest,
			dependencywrite.DependencyMutationAuthorizer) (dependencywrite.DependencyMutationResult, error)
	})
	if !ok {
		t.Fatal("task repository has no transactional dependency writer")
	}
	command := dependencywrite.DependencyMutationRequest{
		Operation: "add", WorkspaceID: "dependency-ws", DependentTaskID: "dependent",
		PredecessorTaskID: "predecessor", OriginClass: "coordinator",
		CallerTaskID: "coordinator-task", CallerSessionID: "session", CallerExecutionID: "execution",
		CorrelationID: "request-1",
	}
	authorize := func(context.Context, *sqlx.Tx) error { return nil }
	first, err := writer.MutateDependency(ctx, command, authorize)
	if err != nil {
		t.Fatalf("first add: %v", err)
	}
	if !first.Changed || first.DependentRevision != 1 || first.PredecessorRevision != 1 {
		t.Fatalf("first result = %+v, want changed and both revisions 1", first)
	}
	command.CorrelationID = "request-2"
	second, err := writer.MutateDependency(ctx, command, authorize)
	if err != nil {
		t.Fatalf("replayed add: %v", err)
	}
	if second.Changed || second.DependentRevision != 1 || second.PredecessorRevision != 1 {
		t.Fatalf("replay result = %+v, want unchanged revisions 1", second)
	}
	var edgeCount, auditCount int
	if err := database.GetContext(ctx, &edgeCount, `SELECT COUNT(*) FROM task_blockers WHERE task_id = ? AND blocker_task_id = ?`, "dependent", "predecessor"); err != nil {
		t.Fatalf("count edge: %v", err)
	}
	if err := database.GetContext(ctx, &auditCount, `SELECT COUNT(*) FROM task_dependency_mutation_audit WHERE dependent_task_id = ? AND predecessor_task_id = ?`, "dependent", "predecessor"); err != nil {
		t.Fatalf("count audits: %v", err)
	}
	if edgeCount != 1 || auditCount != 2 {
		t.Fatalf("edge count %d, audit count %d; want 1 and 2", edgeCount, auditCount)
	}
	command.Operation = "remove"
	command.CorrelationID = "request-3"
	removed, err := writer.MutateDependency(ctx, command, authorize)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !removed.Changed || removed.DependentRevision != 2 || removed.PredecessorRevision != 2 {
		t.Fatalf("remove result = %+v, want changed and both revisions 2", removed)
	}
	command.CorrelationID = "request-4"
	absent, err := writer.MutateDependency(ctx, command, authorize)
	if err != nil {
		t.Fatalf("replayed remove: %v", err)
	}
	if absent.Changed || absent.DependentRevision != 2 || absent.PredecessorRevision != 2 {
		t.Fatalf("absent remove result = %+v, want unchanged revisions 2", absent)
	}
	if err := database.GetContext(ctx, &edgeCount, `SELECT COUNT(*) FROM task_blockers WHERE task_id = ?`, "dependent"); err != nil {
		t.Fatalf("count final edges: %v", err)
	}
	if err := database.GetContext(ctx, &auditCount, `SELECT COUNT(*) FROM task_dependency_mutation_audit WHERE dependent_task_id = ?`, "dependent"); err != nil {
		t.Fatalf("count final audits: %v", err)
	}
	if edgeCount != 0 || auditCount != 4 {
		t.Fatalf("final edge count %d, audit count %d; want 0 and 4", edgeCount, auditCount)
	}
}

// @covers AC-COORDINATOR-DEPENDENCY-002.1
func TestDependencyMutationAuditFailureRollsBackEdge(t *testing.T) {
	repo, _, database := newManagedDeletionSQLitePair(t)
	ctx := t.Context()
	if err := repo.CreateWorkspace(ctx, &models.Workspace{ID: "audit-ws", Name: "Audit"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	for _, id := range []string{"dependent", "predecessor"} {
		if err := repo.CreateTask(ctx, &models.Task{ID: id, WorkspaceID: "audit-ws", Title: id}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	if _, err := database.ExecContext(ctx, `DROP TABLE task_dependency_mutation_audit`); err != nil {
		t.Fatalf("drop audit table: %v", err)
	}
	command := dependencywrite.DependencyMutationRequest{
		Operation: "add", WorkspaceID: "audit-ws", DependentTaskID: "dependent",
		PredecessorTaskID: "predecessor", OriginClass: "coordinator", CorrelationID: "audit-failure",
	}
	_, err := repo.MutateDependency(ctx, command, func(context.Context, *sqlx.Tx) error { return nil })
	if err == nil {
		t.Fatal("missing audit table did not fail mutation")
	}
	var edgeCount, revisionCount int
	if err := database.GetContext(ctx, &edgeCount, `SELECT COUNT(*) FROM task_blockers WHERE task_id = ?`, "dependent"); err != nil {
		t.Fatalf("count edges: %v", err)
	}
	if err := database.GetContext(ctx, &revisionCount, `SELECT COUNT(*) FROM tasks WHERE id IN (?, ?) AND dependency_revision != 0`, "dependent", "predecessor"); err != nil {
		t.Fatalf("count revisions: %v", err)
	}
	if edgeCount != 0 || revisionCount != 0 {
		t.Fatalf("after audit failure: %d edges and %d changed revisions, want zero", edgeCount, revisionCount)
	}
}

// @covers AC-COORDINATOR-DEPENDENCY-001.2
func TestDependencyMutationAuthorizationFailureLeavesGraphUntouched(t *testing.T) {
	repo, _, database := newManagedDeletionSQLitePair(t)
	ctx := t.Context()
	if err := repo.CreateWorkspace(ctx, &models.Workspace{ID: "denial-ws", Name: "Denial"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	for _, id := range []string{"dependent", "predecessor"} {
		if err := repo.CreateTask(ctx, &models.Task{ID: id, WorkspaceID: "denial-ws", Title: id}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	command := dependencywrite.DependencyMutationRequest{
		Operation: "add", WorkspaceID: "denial-ws", DependentTaskID: "dependent",
		PredecessorTaskID: "predecessor", OriginClass: "coordinator", CorrelationID: "denied",
	}
	denied := errors.New("scope revoked")
	_, err := repo.MutateDependency(ctx, command, func(context.Context, *sqlx.Tx) error { return denied })
	if !errors.Is(err, denied) {
		t.Fatalf("mutation error = %v, want scope denial", err)
	}
	var edges int
	if err := database.GetContext(ctx, &edges, `SELECT COUNT(*) FROM task_blockers WHERE task_id = ?`, "dependent"); err != nil {
		t.Fatalf("count edges: %v", err)
	}
	if edges != 0 {
		t.Fatalf("denied mutation wrote %d edges", edges)
	}
	var audit struct {
		Outcome       string `db:"outcome"`
		DependentID   string `db:"dependent_task_id"`
		PredecessorID string `db:"predecessor_task_id"`
	}
	if err := database.GetContext(ctx, &audit,
		`SELECT outcome, dependent_task_id, predecessor_task_id FROM task_dependency_mutation_audit WHERE correlation_id = ?`,
		"denied"); err != nil {
		t.Fatalf("read denial audit: %v", err)
	}
	if audit.Outcome != "denied" || audit.DependentID != "" || audit.PredecessorID != "" {
		t.Fatalf("denial audit disclosed targets or wrong outcome: %+v", audit)
	}
}

// @covers AC-COORDINATOR-DEPENDENCY-002.3
func TestDependencyMutationCycleDenialIsAuditedWithoutTargets(t *testing.T) {
	repo, _, database := newManagedDeletionSQLitePair(t)
	ctx := t.Context()
	if err := repo.CreateWorkspace(ctx, &models.Workspace{ID: "cycle-ws", Name: "Cycle"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	for _, id := range []string{"a", "b"} {
		if err := repo.CreateTask(ctx, &models.Task{ID: id, WorkspaceID: "cycle-ws", Title: id}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	authorize := func(context.Context, *sqlx.Tx) error { return nil }
	command := dependencywrite.DependencyMutationRequest{
		Operation: "add", WorkspaceID: "cycle-ws", DependentTaskID: "a",
		PredecessorTaskID: "b", OriginClass: "coordinator", CorrelationID: "first-edge",
	}
	if _, err := repo.MutateDependency(ctx, command, authorize); err != nil {
		t.Fatalf("seed edge: %v", err)
	}
	command.DependentTaskID, command.PredecessorTaskID = "b", "a"
	command.CorrelationID = "cycle-denied"
	if _, err := repo.MutateDependency(ctx, command, authorize); err == nil {
		t.Fatal("cycle was accepted")
	}
	var audit struct {
		Outcome       string `db:"outcome"`
		DependentID   string `db:"dependent_task_id"`
		PredecessorID string `db:"predecessor_task_id"`
	}
	if err := database.GetContext(ctx, &audit,
		`SELECT outcome, dependent_task_id, predecessor_task_id FROM task_dependency_mutation_audit WHERE correlation_id = ?`,
		"cycle-denied"); err != nil {
		t.Fatalf("read cycle audit: %v", err)
	}
	if audit.Outcome != "denied" || audit.DependentID != "" || audit.PredecessorID != "" {
		t.Fatalf("cycle audit disclosed targets or wrong outcome: %+v", audit)
	}
	var edges int
	if err := database.GetContext(ctx, &edges, `SELECT COUNT(*) FROM task_blockers`); err != nil {
		t.Fatalf("count edges: %v", err)
	}
	if edges != 1 {
		t.Fatalf("cycle left %d edges, want 1", edges)
	}
}

// @covers AC-COORDINATOR-DEPENDENCY-002.2 AC-COORDINATOR-DEPENDENCY-002.4
func TestDependencyMutationConcurrentDuplicateAddChangesRevisionOnce(t *testing.T) {
	repo, _, database := newManagedDeletionSQLitePair(t)
	ctx := t.Context()
	if err := repo.CreateWorkspace(ctx, &models.Workspace{ID: "concurrent-ws", Name: "Concurrent"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	for _, id := range []string{"dependent", "predecessor"} {
		if err := repo.CreateTask(ctx, &models.Task{ID: id, WorkspaceID: "concurrent-ws", Title: id}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	start := make(chan struct{})
	results := make([]dependencywrite.DependencyMutationResult, 2)
	errs := make([]error, 2)
	var group sync.WaitGroup
	for index := range results {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			command := dependencywrite.DependencyMutationRequest{
				Operation: "add", WorkspaceID: "concurrent-ws", DependentTaskID: "dependent",
				PredecessorTaskID: "predecessor", OriginClass: "coordinator",
				CorrelationID: []string{"duplicate-1", "duplicate-2"}[index],
			}
			results[index], errs[index] = repo.MutateDependency(ctx, command,
				func(context.Context, *sqlx.Tx) error { return nil })
		}()
	}
	close(start)
	group.Wait()
	changed := 0
	for index, err := range errs {
		if err != nil {
			t.Fatalf("add %d: %v", index, err)
		}
		if results[index].Changed {
			changed++
		}
		if results[index].DependentRevision != 1 || results[index].PredecessorRevision != 1 {
			t.Fatalf("add %d result = %+v, want revisions 1", index, results[index])
		}
	}
	var edgeCount, auditCount int
	if err := database.GetContext(ctx, &edgeCount, `SELECT COUNT(*) FROM task_blockers WHERE task_id = ?`, "dependent"); err != nil {
		t.Fatalf("count edges: %v", err)
	}
	if err := database.GetContext(ctx, &auditCount, `SELECT COUNT(*) FROM task_dependency_mutation_audit WHERE dependent_task_id = ?`, "dependent"); err != nil {
		t.Fatalf("count audits: %v", err)
	}
	if changed != 1 || edgeCount != 1 || auditCount != 2 {
		t.Fatalf("changed %d, edges %d, audits %d; want 1, 1, 2", changed, edgeCount, auditCount)
	}
}
