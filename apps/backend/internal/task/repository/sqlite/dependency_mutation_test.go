package sqlite_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/task/dependencywrite"
	"github.com/kandev/kandev/internal/task/models"
)

func TestQuickChatExpiryAuditsDependencyCleanup(t *testing.T) {
	repo, _, database := newManagedDeletionSQLitePair(t)
	ctx := t.Context()
	if err := repo.CreateWorkspace(ctx, &models.Workspace{ID: "quick-dependency-ws", Name: "Quick dependencies"}); err != nil {
		t.Fatal(err)
	}
	for _, task := range []*models.Task{
		{ID: "quick", WorkspaceID: "quick-dependency-ws", Title: "quick", IsEphemeral: true},
		{ID: "dependent", WorkspaceID: "quick-dependency-ws", Title: "dependent"},
	} {
		if err := repo.CreateTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.ExecContext(ctx,
		`UPDATE tasks SET updated_at = ? WHERE id = 'quick'`, time.Now().Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	request := dependencywrite.DependencyMutationRequest{
		Operation: "add", WorkspaceID: "quick-dependency-ws", DependentTaskID: "dependent",
		PredecessorTaskID: "quick", OriginClass: "task_service", CorrelationID: "seed",
	}
	if _, err := repo.MutateDependency(ctx, request, func(context.Context, *sqlx.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	deleted, snapshots, err := repo.DeleteExpiredQuickChatTaskWithDependencySnapshots(ctx, "quick", time.Now().Add(-24*time.Hour))
	if err != nil || !deleted {
		t.Fatalf("quick expiry: deleted=%v err=%v", deleted, err)
	}
	if len(snapshots) != 1 || snapshots[0].TaskID != "dependent" || snapshots[0].Revision != 2 || snapshots[0].Blocked {
		t.Fatalf("survivor snapshot = %+v", snapshots)
	}
	var auditCount, edgeCount int
	if err := database.GetContext(ctx, &auditCount,
		`SELECT COUNT(*) FROM task_dependency_mutation_audit WHERE origin_class = 'task_delete' AND dependent_task_id = 'dependent'`); err != nil {
		t.Fatal(err)
	}
	if err := database.GetContext(ctx, &edgeCount, `SELECT COUNT(*) FROM task_blockers WHERE task_id = 'dependent'`); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 || edgeCount != 0 {
		t.Fatalf("cleanup audits=%d edges=%d; want 1,0", auditCount, edgeCount)
	}
}

func TestIneligibleQuickChatExpiryPreservesDependency(t *testing.T) {
	repo, _, database := newManagedDeletionSQLitePair(t)
	ctx := t.Context()
	if err := repo.CreateWorkspace(ctx, &models.Workspace{ID: "quick-recent-ws", Name: "Recent quick chat"}); err != nil {
		t.Fatal(err)
	}
	for _, task := range []*models.Task{
		{ID: "quick", WorkspaceID: "quick-recent-ws", Title: "quick", IsEphemeral: true},
		{ID: "dependent", WorkspaceID: "quick-recent-ws", Title: "dependent"},
	} {
		if err := repo.CreateTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	request := dependencywrite.DependencyMutationRequest{
		Operation: "add", WorkspaceID: "quick-recent-ws", DependentTaskID: "dependent",
		PredecessorTaskID: "quick", OriginClass: "task_service", CorrelationID: "seed",
	}
	if _, err := repo.MutateDependency(ctx, request, func(context.Context, *sqlx.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	deleted, snapshots, err := repo.DeleteExpiredQuickChatTaskWithDependencySnapshots(ctx, "quick", time.Now().Add(-24*time.Hour))
	if err != nil || deleted || len(snapshots) != 0 {
		t.Fatalf("ineligible expiry: deleted=%v snapshots=%+v err=%v", deleted, snapshots, err)
	}
	var edgeCount, auditCount int
	if err := database.GetContext(ctx, &edgeCount, `SELECT COUNT(*) FROM task_blockers WHERE task_id = 'dependent'`); err != nil {
		t.Fatal(err)
	}
	if err := database.GetContext(ctx, &auditCount,
		`SELECT COUNT(*) FROM task_dependency_mutation_audit WHERE origin_class = 'task_delete'`); err != nil {
		t.Fatal(err)
	}
	if edgeCount != 1 || auditCount != 0 {
		t.Fatalf("ineligible cleanup: edges=%d audits=%d; want 1,0", edgeCount, auditCount)
	}
}

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
	if len(first.Dependent.DependsOn) != 1 || first.Dependent.DependsOn[0].ID != "predecessor" ||
		len(first.Predecessor.Blocks) != 1 || first.Predecessor.Blocks[0].ID != "dependent" {
		t.Fatalf("first snapshots = %+v / %+v", first.Dependent, first.Predecessor)
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
	if len(removed.Dependent.DependsOn) != 0 || len(removed.Predecessor.Blocks) != 0 {
		t.Fatalf("removed snapshots retain edge: %+v / %+v", removed.Dependent, removed.Predecessor)
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

func TestDependencyReplacementRollsBackWholeSetOnCycle(t *testing.T) {
	repo, _, database := newManagedDeletionSQLitePair(t)
	ctx := t.Context()
	if err := repo.CreateWorkspace(ctx, &models.Workspace{ID: "replace-ws", Name: "Replace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	for _, id := range []string{"dependent", "first", "second"} {
		if err := repo.CreateTask(ctx, &models.Task{ID: id, WorkspaceID: "replace-ws", Title: id}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	authorize := func(context.Context, *sqlx.Tx) error { return nil }
	base := dependencywrite.DependencyMutationRequest{
		WorkspaceID: "replace-ws", DependentTaskID: "dependent",
		OriginClass: "task_service", CorrelationID: "replace-base",
	}
	if _, err := repo.ReplaceDependencySet(ctx, base, []string{"first"}, authorize); err != nil {
		t.Fatalf("seed replacement: %v", err)
	}
	reverse := dependencywrite.DependencyMutationRequest{
		Operation: "add", WorkspaceID: "replace-ws", DependentTaskID: "second",
		PredecessorTaskID: "dependent", OriginClass: "task_service", CorrelationID: "reverse",
	}
	if _, err := repo.MutateDependency(ctx, reverse, authorize); err != nil {
		t.Fatalf("seed reverse edge: %v", err)
	}
	base.CorrelationID = "replace-cycle"
	if _, err := repo.ReplaceDependencySet(ctx, base, []string{"second"}, authorize); !errors.Is(err, dependencywrite.ErrCycle) {
		t.Fatalf("cyclic replacement = %v, want cycle", err)
	}
	var oldEdge, newEdge, cycleAudit int
	for _, check := range []struct {
		dest  *int
		query string
		args  []any
	}{
		{&oldEdge, `SELECT COUNT(*) FROM task_blockers WHERE task_id = ? AND blocker_task_id = ?`, []any{"dependent", "first"}},
		{&newEdge, `SELECT COUNT(*) FROM task_blockers WHERE task_id = ? AND blocker_task_id = ?`, []any{"dependent", "second"}},
		{&cycleAudit, `SELECT COUNT(*) FROM task_dependency_mutation_audit WHERE correlation_id = ?`, []any{"replace-cycle"}},
	} {
		if err := database.GetContext(ctx, check.dest, check.query, check.args...); err != nil {
			t.Fatalf("read replacement state: %v", err)
		}
	}
	if oldEdge != 1 || newEdge != 0 || cycleAudit != 0 {
		t.Fatalf("after cycle: old edge %d, new edge %d, audit %d; want 1, 0, 0", oldEdge, newEdge, cycleAudit)
	}
}

func TestTaskDeleteCommitsEdgeCleanupAndSurvivorSnapshot(t *testing.T) {
	repo, _, database := newManagedDeletionSQLitePair(t)
	ctx := t.Context()
	if err := repo.CreateWorkspace(ctx, &models.Workspace{ID: "delete-ws", Name: "Delete"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	for _, id := range []string{"dependent", "predecessor"} {
		if err := repo.CreateTask(ctx, &models.Task{ID: id, WorkspaceID: "delete-ws", Title: id}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	request := dependencywrite.DependencyMutationRequest{
		Operation: "add", WorkspaceID: "delete-ws", DependentTaskID: "dependent",
		PredecessorTaskID: "predecessor", OriginClass: "task_service", CorrelationID: "seed",
	}
	if _, err := repo.MutateDependency(ctx, request, func(context.Context, *sqlx.Tx) error { return nil }); err != nil {
		t.Fatalf("seed dependency: %v", err)
	}
	snapshots, err := repo.DeleteTaskWithDependencySnapshots(ctx, "predecessor")
	if err != nil {
		t.Fatalf("delete predecessor: %v", err)
	}
	if len(snapshots) != 1 || snapshots[0].TaskID != "dependent" || snapshots[0].Revision != 2 || snapshots[0].Blocked {
		t.Fatalf("survivor snapshots = %+v, want dependent revision 2 unblocked", snapshots)
	}
	var edgeCount, auditCount int
	if err := database.GetContext(ctx, &edgeCount, `SELECT COUNT(*) FROM task_blockers WHERE blocker_task_id = 'predecessor'`); err != nil {
		t.Fatalf("count dangling edges: %v", err)
	}
	if err := database.GetContext(ctx, &auditCount,
		`SELECT COUNT(*) FROM task_dependency_mutation_audit WHERE origin_class = 'task_delete' AND dependent_task_id = 'dependent'`); err != nil {
		t.Fatalf("count cleanup audit: %v", err)
	}
	if edgeCount != 0 || auditCount != 1 {
		t.Fatalf("dangling edges = %d, cleanup audits = %d; want 0 and 1", edgeCount, auditCount)
	}
}

func TestTaskDeleteRollsBackWhenDependencyAuditFails(t *testing.T) {
	repo, _, database := newManagedDeletionSQLitePair(t)
	ctx := t.Context()
	if err := repo.CreateWorkspace(ctx, &models.Workspace{ID: "delete-audit-ws", Name: "Delete audit"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	for _, id := range []string{"dependent", "predecessor"} {
		if err := repo.CreateTask(ctx, &models.Task{ID: id, WorkspaceID: "delete-audit-ws", Title: id}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	request := dependencywrite.DependencyMutationRequest{
		Operation: "add", WorkspaceID: "delete-audit-ws", DependentTaskID: "dependent",
		PredecessorTaskID: "predecessor", OriginClass: "task_service", CorrelationID: "seed",
	}
	if _, err := repo.MutateDependency(ctx, request, func(context.Context, *sqlx.Tx) error { return nil }); err != nil {
		t.Fatalf("seed dependency: %v", err)
	}
	if _, err := database.ExecContext(ctx, `DROP TABLE task_dependency_mutation_audit`); err != nil {
		t.Fatalf("drop audit: %v", err)
	}
	if err := repo.DeleteTask(ctx, "predecessor"); err == nil {
		t.Fatal("delete succeeded without durable dependency audit")
	}
	var taskCount, edgeCount, revision int
	if err := database.GetContext(ctx, &taskCount, `SELECT COUNT(*) FROM tasks WHERE id = 'predecessor'`); err != nil {
		t.Fatalf("read task: %v", err)
	}
	if err := database.GetContext(ctx, &edgeCount, `SELECT COUNT(*) FROM task_blockers WHERE task_id = 'dependent' AND blocker_task_id = 'predecessor'`); err != nil {
		t.Fatalf("read edge: %v", err)
	}
	if err := database.GetContext(ctx, &revision, `SELECT dependency_revision FROM tasks WHERE id = 'dependent'`); err != nil {
		t.Fatalf("read revision: %v", err)
	}
	if taskCount != 1 || edgeCount != 1 || revision != 1 {
		t.Fatalf("after audit failure: task %d, edge %d, revision %d; want 1 each", taskCount, edgeCount, revision)
	}
}

func TestTaskDeleteCleansLegacyDanglingEdge(t *testing.T) {
	repo, _, database := newManagedDeletionSQLitePair(t)
	ctx := t.Context()
	if err := repo.CreateWorkspace(ctx, &models.Workspace{ID: "dangling-ws", Name: "Dangling"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := repo.CreateTask(ctx, &models.Task{ID: "dependent", WorkspaceID: "dangling-ws", Title: "Dependent"}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := database.ExecContext(ctx,
		`INSERT INTO task_blockers(task_id, blocker_task_id, created_at) VALUES ('dependent', 'missing', CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("seed dangling edge: %v", err)
	}
	if err := repo.DeleteTask(ctx, "dependent"); err != nil {
		t.Fatalf("delete with dangling edge: %v", err)
	}
	var edges, audit int
	if err := database.GetContext(ctx, &edges, `SELECT COUNT(*) FROM task_blockers WHERE task_id = 'dependent'`); err != nil {
		t.Fatalf("read edge: %v", err)
	}
	if err := database.GetContext(ctx, &audit,
		`SELECT COUNT(*) FROM task_dependency_mutation_audit WHERE origin_class = 'task_delete' AND reason_code = 'legacy_cleanup' AND predecessor_id_canonical = 0`); err != nil {
		t.Fatalf("read cleanup audit: %v", err)
	}
	if edges != 0 || audit != 1 {
		t.Fatalf("after delete: edges %d, legacy audits %d; want 0 and 1", edges, audit)
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
