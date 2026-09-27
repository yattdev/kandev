package sqlite

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/task/models"
)

// @covers AC-TASKS-SAFE-FORCE-REMOVAL-004.1
// @covers AC-TASKS-SAFE-FORCE-REMOVAL-004.2
func TestClaimForceRemovalRejectsStaleOrForeignTaskAndHoldsCleanup(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-ws", Name: "Force"}))
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "force-task", WorkspaceID: "force-ws", Title: "Force"}))
	task, err := repo.GetTask(ctx, "force-task")
	require.NoError(t, err)

	claim := &models.ForceRemovalClaim{TaskID: task.ID, WorkspaceID: task.WorkspaceID, TaskGeneration: task.UpdatedAt, AdmissionGeneration: "admission", OperationID: "operation", RequestDigest: "request", PreviewDigest: "preview"}
	stored, replay, err := repo.ClaimForceRemoval(ctx, claim)
	require.NoError(t, err)
	require.False(t, replay)
	require.Equal(t, claim, stored)
	require.NoError(t, repo.AppendForceRemovalReceipt(ctx, claim.OperationID, models.ExactRetirementPredicateReceipt{Predicate: models.ExactRetirementIdentityPredicate, Status: models.ExactRetirementReceiptPass, ReasonCode: "EXACT_TASK_CLAIMED", ResourceID: task.ID, ObservedGeneration: task.UpdatedAt.UTC().Format(time.RFC3339Nano), EvidenceDigest: "digest"}))
	receipts, err := repo.ListForceRemovalReceipts(ctx, claim.OperationID)
	require.NoError(t, err)
	require.Len(t, receipts, 1)
	require.Equal(t, "EXACT_TASK_CLAIMED", receipts[0].ReasonCode)

	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: task.ID, WorkspaceID: "foreign", TaskGeneration: task.UpdatedAt, AdmissionGeneration: "admission", OperationID: "other", RequestDigest: "request", PreviewDigest: "preview"})
	require.ErrorIs(t, err, ErrForceRemovalClaimStale)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: task.ID, WorkspaceID: task.WorkspaceID, TaskGeneration: task.UpdatedAt, AdmissionGeneration: "admission", OperationID: "operation", RequestDigest: "different", PreviewDigest: "preview"})
	require.ErrorIs(t, err, ErrForceRemovalClaimConflict)

	err = repo.CreateTaskResourceCleanupJob(ctx, &models.TaskResourceCleanupJob{TaskID: task.ID, OperationID: "cleanup", Trigger: models.TaskResourceCleanupTriggerDelete, ResourceSnapshot: `{}`})
	require.ErrorIs(t, err, ErrForceRemovalCleanupHeld)
	jobs, err := repo.ListTaskResourceCleanupJobs(ctx, task.ID)
	require.NoError(t, err)
	require.Empty(t, jobs)

	err = repo.CreateTaskSession(ctx, &models.TaskSession{ID: "force-session", TaskID: task.ID})
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	sessions, err := repo.ListTaskSessions(ctx, task.ID)
	require.NoError(t, err)
	require.Empty(t, sessions)

	err = repo.CreateTaskSessionWithWorkspaceBinding(ctx, &models.TaskSession{ID: "force-worktree-session", TaskID: task.ID}, &models.TaskEnvironment{ID: "force-environment", TaskID: task.ID})
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	environment, err := repo.GetTaskEnvironmentByTaskID(ctx, task.ID)
	require.NoError(t, err)
	require.Nil(t, environment)
}

// @covers AC-TASKS-SAFE-FORCE-REMOVAL-004.1
func TestClaimForceRemovalOperationCannotBeReusedForAnotherTask(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-op-ws", Name: "Force"}))
	for _, taskID := range []string{"force-op-first", "force-op-second"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-op-ws", Title: taskID}))
	}
	first, err := repo.GetTask(ctx, "force-op-first")
	require.NoError(t, err)
	second, err := repo.GetTask(ctx, "force-op-second")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: first.ID, WorkspaceID: first.WorkspaceID, TaskGeneration: first.UpdatedAt, AdmissionGeneration: "admission", OperationID: "shared-operation", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: second.ID, WorkspaceID: second.WorkspaceID, TaskGeneration: second.UpdatedAt, AdmissionGeneration: "admission", OperationID: "shared-operation", RequestDigest: "request", PreviewDigest: "preview"})
	require.ErrorIs(t, err, ErrForceRemovalClaimConflict)
}

// @covers AC-TASKS-SAFE-FORCE-REMOVAL-004.1
func TestClaimForceRemovalConcurrentExactRequestReplays(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-race-ws", Name: "Force"}))
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "force-race-task", WorkspaceID: "force-race-ws", Title: "Force"}))
	task, err := repo.GetTask(ctx, "force-race-task")
	require.NoError(t, err)

	claim := models.ForceRemovalClaim{TaskID: task.ID, WorkspaceID: task.WorkspaceID, TaskGeneration: task.UpdatedAt, AdmissionGeneration: "admission", OperationID: "operation", RequestDigest: "request", PreviewDigest: "preview"}
	start := make(chan struct{})
	results := make(chan bool, 2)
	errs := make(chan error, 2)
	var callers sync.WaitGroup
	for range 2 {
		callers.Add(1)
		go func() {
			defer callers.Done()
			<-start
			attempt := claim
			_, replay, err := repo.ClaimForceRemoval(ctx, &attempt)
			if err != nil {
				errs <- err
				return
			}
			results <- replay
		}()
	}
	close(start)
	callers.Wait()
	close(results)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var replays int
	for replay := range results {
		if replay {
			replays++
		}
	}
	require.Equal(t, 1, replays)
}

// @covers AC-TASKS-SAFE-FORCE-REMOVAL-004.2
func TestAppendForceRemovalReceiptSerializesAndReplaysExactPredicate(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-receipt-ws", Name: "Force"}))
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "force-receipt-task", WorkspaceID: "force-receipt-ws", Title: "Force"}))
	task, err := repo.GetTask(ctx, "force-receipt-task")
	require.NoError(t, err)
	claim := &models.ForceRemovalClaim{TaskID: task.ID, WorkspaceID: task.WorkspaceID, TaskGeneration: task.UpdatedAt, AdmissionGeneration: "admission", OperationID: "receipt-operation", RequestDigest: "request", PreviewDigest: "preview"}
	_, _, err = repo.ClaimForceRemoval(ctx, claim)
	require.NoError(t, err)

	identity := models.ExactRetirementPredicateReceipt{Predicate: models.ExactRetirementIdentityPredicate, Status: models.ExactRetirementReceiptPass, ReasonCode: "EXACT_TASK_CLAIMED", ResourceID: task.ID, ObservedGeneration: "generation", EvidenceDigest: "identity-digest"}
	require.NoError(t, repo.AppendForceRemovalReceipt(ctx, claim.OperationID, identity))
	require.NoError(t, repo.AppendForceRemovalReceipt(ctx, claim.OperationID, identity))
	changed := identity
	changed.EvidenceDigest = "changed-digest"
	require.ErrorIs(t, repo.AppendForceRemovalReceipt(ctx, claim.OperationID, changed), ErrForceRemovalClaimConflict)
	require.ErrorIs(t, repo.AppendForceRemovalReceipt(ctx, "foreign-operation", identity), ErrForceRemovalClaimStale)

	start := make(chan struct{})
	errs := make(chan error, 2)
	var appenders sync.WaitGroup
	for _, receipt := range []models.ExactRetirementPredicateReceipt{
		{Predicate: models.ExactRetirementQueuePredicate, Status: models.ExactRetirementReceiptPass, ReasonCode: "QUEUE_RETAINED", ResourceID: task.ID, ObservedGeneration: "generation", EvidenceDigest: "queue-digest"},
		{Predicate: models.ExactRetirementMovePredicate, Status: models.ExactRetirementReceiptPass, ReasonCode: "DISPATCH_HELD", ResourceID: task.ID, ObservedGeneration: "generation", EvidenceDigest: "dispatch-digest"},
	} {
		appenders.Add(1)
		go func(receipt models.ExactRetirementPredicateReceipt) {
			defer appenders.Done()
			<-start
			errs <- repo.AppendForceRemovalReceipt(ctx, claim.OperationID, receipt)
		}(receipt)
	}
	close(start)
	appenders.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	receipts, err := repo.ListForceRemovalReceipts(ctx, claim.OperationID)
	require.NoError(t, err)
	require.Len(t, receipts, 3)
}

// @covers AC-TASKS-SAFE-FORCE-REMOVAL-004.1
func TestClaimForceRemovalBlocksMessageWithoutPersistingIt(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-message-ws", Name: "Force"}))
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "force-message-task", WorkspaceID: "force-message-ws", Title: "Force"}))
	task, err := repo.GetTask(ctx, "force-message-task")
	require.NoError(t, err)
	require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: "force-message-session", TaskID: task.ID}))
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: task.ID, WorkspaceID: task.WorkspaceID, TaskGeneration: task.UpdatedAt, AdmissionGeneration: "admission", OperationID: "message-operation", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)

	err = repo.CreateMessage(ctx, &models.Message{ID: "force-message", TaskSessionID: "force-message-session", TaskID: task.ID, Content: "retained"})
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	messages, err := repo.ListMessages(ctx, "force-message-session")
	require.NoError(t, err)
	require.Empty(t, messages)
}

// @covers AC-TASKS-SAFE-FORCE-REMOVAL-004.1
func TestClaimForceRemovalBlocksMoveWriterWithoutPersistingIt(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-move-ws", Name: "Force"}))
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "force-move-task", WorkspaceID: "force-move-ws", Title: "Original"}))
	task, err := repo.GetTask(ctx, "force-move-task")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: task.ID, WorkspaceID: task.WorkspaceID, TaskGeneration: task.UpdatedAt, AdmissionGeneration: "admission", OperationID: "move-operation", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)

	task.Title = "Changed"
	require.ErrorIs(t, repo.UpdateTask(ctx, task), ErrForceRemovalTaskHeld)
	stored, err := repo.GetTask(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, "Original", stored.Title)
}

// @covers AC-TASKS-SAFE-FORCE-REMOVAL-004.1
// @covers AC-TASKS-SAFE-FORCE-REMOVAL-004.2
func TestClaimForceRemovalBlocksEnvironmentAndCleanupWorkerAdmissions(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-environment-ws", Name: "Force"}))
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "force-environment-task", WorkspaceID: "force-environment-ws", Title: "Force"}))
	require.NoError(t, repo.CreateTaskEnvironment(ctx, &models.TaskEnvironment{ID: "force-environment", TaskID: "force-environment-task", Status: models.TaskEnvironmentStatusCreating}))
	require.NoError(t, repo.CreateTaskResourceCleanupJob(ctx, &models.TaskResourceCleanupJob{ID: "force-cleanup-pending", TaskID: "force-environment-task", OperationID: "force-cleanup-pending-operation", Trigger: models.TaskResourceCleanupTriggerDelete, ResourceSnapshot: `{}`}))
	require.NoError(t, repo.CreateTaskResourceCleanupJob(ctx, &models.TaskResourceCleanupJob{ID: "force-cleanup-prepared", TaskID: "force-environment-task", OperationID: "force-cleanup-prepared-operation", Trigger: models.TaskResourceCleanupTriggerDelete, State: models.TaskResourceCleanupStatePrepared, ResourceSnapshot: `{}`}))
	task, err := repo.GetTask(ctx, "force-environment-task")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: task.ID, WorkspaceID: task.WorkspaceID, TaskGeneration: task.UpdatedAt, AdmissionGeneration: "admission", OperationID: "environment-operation", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)

	updated := &models.TaskEnvironment{ID: "force-environment", TaskID: task.ID, Status: models.TaskEnvironmentStatusFailed}
	require.ErrorIs(t, repo.UpdateTaskEnvironment(ctx, updated), ErrForceRemovalTaskHeld)
	require.ErrorIs(t, repo.CreateTaskEnvironment(ctx, &models.TaskEnvironment{ID: "force-environment-new", TaskID: task.ID, Status: models.TaskEnvironmentStatusCreating}), ErrForceRemovalTaskHeld)
	require.ErrorIs(t, repo.DeleteTaskEnvironment(ctx, "force-environment"), ErrForceRemovalTaskHeld)
	environment, err := repo.GetTaskEnvironment(ctx, "force-environment")
	require.NoError(t, err)
	require.Equal(t, models.TaskEnvironmentStatusCreating, environment.Status)
	_, err = repo.GetTaskEnvironment(ctx, "force-environment-new")
	require.ErrorIs(t, err, ErrTaskEnvironmentNotFound)

	running, err := repo.MarkTaskResourceCleanupJobRunning(ctx, "force-cleanup-pending")
	require.ErrorIs(t, err, ErrForceRemovalCleanupHeld)
	require.False(t, running)
	started, err := repo.StartPreparedTaskResourceCleanupJob(ctx, "force-cleanup-prepared")
	require.ErrorIs(t, err, ErrForceRemovalCleanupHeld)
	require.False(t, started)
	pending, err := repo.GetTaskResourceCleanupJob(ctx, "force-cleanup-pending")
	require.NoError(t, err)
	require.Equal(t, models.TaskResourceCleanupStatePending, pending.State)
	prepared, err := repo.GetTaskResourceCleanupJob(ctx, "force-cleanup-prepared")
	require.NoError(t, err)
	require.Equal(t, models.TaskResourceCleanupStatePrepared, prepared.State)
}

// @covers AC-TASKS-SAFE-FORCE-REMOVAL-004.1
// @covers AC-TASKS-SAFE-FORCE-REMOVAL-004.2
func TestClaimForceRemovalPreservesQueueAndPendingMoveRows(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-queue-ws", Name: "Force"}))
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "force-queue-task", WorkspaceID: "force-queue-ws", Title: "Force"}))
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "force-queue-foreign-task", WorkspaceID: "force-queue-ws", Title: "Foreign"}))
	require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: "force-queue-session", TaskID: "force-queue-task"}))
	require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: "force-queue-foreign-session", TaskID: "force-queue-foreign-task"}))

	queueRepo, err := messagequeue.NewSQLiteRepository(repo.db, repo.db)
	require.NoError(t, err)
	identity, err := queueRepo.ResolveSessionIdentity(ctx, "force-queue-task", "force-queue-session")
	require.NoError(t, err)
	foreignIdentity, err := queueRepo.ResolveSessionIdentity(ctx, "force-queue-foreign-task", "force-queue-foreign-session")
	require.NoError(t, err)
	entry := &messagequeue.QueuedMessage{ID: "force-queue-entry", SessionID: identity.SessionID, TaskID: identity.TaskID, Content: "retained", QueuedBy: messagequeue.QueuedByUser}
	require.NoError(t, queueRepo.InsertForSession(ctx, identity, entry, messagequeue.DefaultMaxPerSession))
	pending := &messagequeue.PendingMove{MoveID: "force-pending-move", TaskID: identity.TaskID, WorkflowID: "workflow", WorkflowStepID: "step"}
	require.NoError(t, queueRepo.SetPendingMove(ctx, identity.SessionID, pending))

	task, err := repo.GetTask(ctx, identity.TaskID)
	require.NoError(t, err)
	claim := &models.ForceRemovalClaim{TaskID: task.ID, WorkspaceID: task.WorkspaceID, TaskGeneration: task.UpdatedAt, AdmissionGeneration: "admission", OperationID: "queue-operation", RequestDigest: "request", PreviewDigest: "preview"}
	_, _, err = repo.ClaimForceRemoval(ctx, claim)
	require.NoError(t, err)

	err = queueRepo.InsertForSession(ctx, identity, &messagequeue.QueuedMessage{ID: "force-queue-new", SessionID: identity.SessionID, TaskID: identity.TaskID, Content: "blocked", QueuedBy: messagequeue.QueuedByUser}, messagequeue.DefaultMaxPerSession)
	require.ErrorIs(t, err, models.ErrForceRemovalTaskHeld)
	_, err = queueRepo.ClaimSendNowForSession(ctx, identity, []messagequeue.QueuedMessage{*entry})
	require.ErrorIs(t, err, models.ErrForceRemovalTaskHeld)
	_, _, err = queueRepo.AppendOrInsertTailForSession(ctx, identity, "blocked append", "", messagequeue.QueuedByUser, false, nil, nil, messagequeue.DefaultMaxPerSession)
	require.ErrorIs(t, err, models.ErrForceRemovalTaskHeld)
	_, err = queueRepo.TakeByIDForSession(ctx, identity, entry.ID)
	require.ErrorIs(t, err, models.ErrForceRemovalTaskHeld)
	_, err = queueRepo.TakeHead(ctx, identity.SessionID)
	require.ErrorIs(t, err, models.ErrForceRemovalTaskHeld)
	require.ErrorIs(t, queueRepo.SetPendingMove(ctx, identity.SessionID, &messagequeue.PendingMove{MoveID: "force-pending-replacement", TaskID: identity.TaskID}), models.ErrForceRemovalTaskHeld)
	_, err = queueRepo.TakePendingMove(ctx, identity.SessionID)
	require.ErrorIs(t, err, models.ErrForceRemovalTaskHeld)

	entries, err := queueRepo.ListBySession(ctx, identity.SessionID)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, entry.ID, entries[0].ID)
	storedPending, err := queueRepo.GetPendingMove(ctx, identity.SessionID)
	require.NoError(t, err)
	require.NotNil(t, storedPending)
	require.Equal(t, pending.MoveID, storedPending.MoveID)

	require.NoError(t, queueRepo.InsertForSession(ctx, foreignIdentity, &messagequeue.QueuedMessage{ID: "force-queue-foreign-entry", SessionID: foreignIdentity.SessionID, TaskID: foreignIdentity.TaskID, Content: "allowed", QueuedBy: messagequeue.QueuedByUser}, messagequeue.DefaultMaxPerSession))
	foreignEntries, err := queueRepo.ListBySession(ctx, foreignIdentity.SessionID)
	require.NoError(t, err)
	require.Len(t, foreignEntries, 1)
	foreignDispatch, err := queueRepo.ClaimSendNowForSession(ctx, foreignIdentity, foreignEntries)
	require.NoError(t, err)
	require.NotNil(t, foreignDispatch)
}

// @covers AC-TASKS-SAFE-FORCE-REMOVAL-004.1
func TestClaimForceRemovalRejectsPendingMoveSweepWithoutDeletingRetainedRows(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-sweep-ws", Name: "Force"}))
	for _, taskID := range []string{"force-sweep-task", "force-sweep-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-sweep-ws", Title: taskID}))
	}
	for _, session := range []*models.TaskSession{{ID: "force-sweep-session", TaskID: "force-sweep-task"}, {ID: "force-sweep-foreign-session", TaskID: "force-sweep-foreign"}} {
		require.NoError(t, repo.CreateTaskSession(ctx, session))
	}
	queueRepo, err := messagequeue.NewSQLiteRepository(repo.db, repo.db)
	require.NoError(t, err)
	for _, entry := range []*messagequeue.QueuedMessage{
		{ID: "force-sweep-handoff", SessionID: "force-sweep-session", TaskID: "force-sweep-task", Content: "retained", QueuedBy: messagequeue.QueuedByMoveTask},
		{ID: "force-sweep-foreign-handoff", SessionID: "force-sweep-foreign-session", TaskID: "force-sweep-foreign", Content: "foreign", QueuedBy: messagequeue.QueuedByMoveTask},
	} {
		require.NoError(t, queueRepo.Insert(ctx, entry, messagequeue.DefaultMaxPerSession))
	}
	require.NoError(t, queueRepo.SetPendingMove(ctx, "force-sweep-session", &messagequeue.PendingMove{MoveID: "force-sweep-move", TaskID: "force-sweep-task", WorkflowID: "workflow", WorkflowStepID: "step", QueuedAt: time.Now().UTC()}))
	require.NoError(t, queueRepo.SetPendingMove(ctx, "force-sweep-foreign-session", &messagequeue.PendingMove{MoveID: "force-sweep-foreign-move", TaskID: "force-sweep-foreign", WorkflowID: "workflow", WorkflowStepID: "step", QueuedAt: time.Now().UTC()}))
	records, err := queueRepo.ListPendingMoves(ctx)
	require.NoError(t, err)
	require.Len(t, records, 2)

	task, err := repo.GetTask(ctx, "force-sweep-task")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: task.ID, WorkspaceID: task.WorkspaceID, TaskGeneration: task.UpdatedAt, AdmissionGeneration: "admission", OperationID: "sweep-operation", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)

	var claimed, foreign messagequeue.PendingMoveRecord
	for _, record := range records {
		if record.SessionID == "force-sweep-session" {
			claimed = record
		} else {
			foreign = record
		}
	}
	removed, err := queueRepo.DeletePendingMoveIfMatch(ctx, claimed, "force-sweep-handoff")
	require.ErrorIs(t, err, models.ErrForceRemovalTaskHeld)
	require.False(t, removed)
	pending, err := queueRepo.GetPendingMove(ctx, claimed.SessionID)
	require.NoError(t, err)
	require.NotNil(t, pending)
	entries, err := queueRepo.ListBySession(ctx, claimed.SessionID)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "force-sweep-handoff", entries[0].ID)

	removed, err = queueRepo.DeletePendingMoveIfMatch(ctx, foreign, "force-sweep-foreign-handoff")
	require.NoError(t, err)
	require.True(t, removed)
}

// @covers AC-TASKS-SAFE-FORCE-REMOVAL-004.1
func TestClaimForceRemovalPreservesDeferredLaunchRecord(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-deferred-ws", Name: "Force"}))
	require.NoError(t, repo.CreateTask(ctx, &models.Task{
		ID:          "force-deferred-task",
		WorkspaceID: "force-deferred-ws",
		Title:       "Force",
		Metadata: map[string]interface{}{models.MetaKeyDeferredLaunch: map[string]interface{}{
			models.DeferredLaunchStartWhenUnblockedKey: true,
			models.DeferredLaunchUserIDKey:             "operator",
		}},
	}))
	task, err := repo.GetTask(ctx, "force-deferred-task")
	require.NoError(t, err)
	_, prior, err := repo.GetTaskDeferredLaunch(ctx, task.ID)
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: task.ID, WorkspaceID: task.WorkspaceID, TaskGeneration: task.UpdatedAt, AdmissionGeneration: "admission", OperationID: "deferred-operation", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)

	stored, lost, err := repo.SetTaskDeferredLaunchIfUnchanged(ctx, task.ID, prior, map[string]interface{}{models.CeilingDeferredKey: true})
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	require.False(t, stored)
	require.False(t, lost)
	_, claimed, err := repo.TakeTaskDeferredLaunchWIPKeys(ctx, task.ID)
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	require.False(t, claimed)
	require.ErrorIs(t, repo.RestoreTaskDeferredLaunchWIPKeys(ctx, task.ID, map[string]interface{}{models.DeferredLaunchUserIDKey: "replacement"}), ErrForceRemovalTaskHeld)

	record, _, err := repo.GetTaskDeferredLaunch(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, true, record[models.DeferredLaunchStartWhenUnblockedKey])
	require.Equal(t, "operator", record[models.DeferredLaunchUserIDKey])
}

// @covers AC-TASKS-SAFE-FORCE-REMOVAL-004.1
func TestClaimForceRemovalPreservesSessionTransferAttachments(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-transfer-ws", Name: "Force"}))
	for _, taskID := range []string{"force-transfer-task", "force-transfer-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-transfer-ws", Title: taskID}))
	}
	for _, session := range []*models.TaskSession{
		{ID: "force-transfer-source", TaskID: "force-transfer-task"},
		{ID: "force-transfer-destination", TaskID: "force-transfer-task"},
		{ID: "force-transfer-foreign-source", TaskID: "force-transfer-foreign"},
		{ID: "force-transfer-foreign-destination", TaskID: "force-transfer-foreign"},
	} {
		require.NoError(t, repo.CreateTaskSession(ctx, session))
	}
	now := time.Now().UTC()
	for _, attachment := range []*models.TaskMessageAttachment{
		{ID: "force-transfer-attachment", OwnerID: "operator", WorkspaceID: "force-transfer-ws", TaskID: "force-transfer-task", SessionID: "force-transfer-source", Name: "retained", MimeType: "text/plain", Kind: "resource", DeliveryMode: "path", SizeBytes: 1, StorageKey: "force-transfer-attachment", State: models.AttachmentStateClaimed, ExpiresAt: now.Add(time.Hour), CreatedAt: now},
		{ID: "force-transfer-foreign-attachment", OwnerID: "operator", WorkspaceID: "force-transfer-ws", TaskID: "force-transfer-foreign", SessionID: "force-transfer-foreign-source", Name: "allowed", MimeType: "text/plain", Kind: "resource", DeliveryMode: "path", SizeBytes: 1, StorageKey: "force-transfer-foreign-attachment", State: models.AttachmentStateClaimed, ExpiresAt: now.Add(time.Hour), CreatedAt: now},
	} {
		require.NoError(t, repo.CreateMessageAttachment(ctx, attachment))
	}
	task, err := repo.GetTask(ctx, "force-transfer-task")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: task.ID, WorkspaceID: task.WorkspaceID, TaskGeneration: task.UpdatedAt, AdmissionGeneration: "admission", OperationID: "transfer-operation", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)

	err = repo.TransferMessageAttachments(ctx, task.ID, "force-transfer-source", "force-transfer-destination", []string{"force-transfer-attachment"})
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	retained, err := repo.GetMessageAttachment(ctx, "force-transfer-attachment")
	require.NoError(t, err)
	require.Equal(t, "force-transfer-source", retained.SessionID)
	require.NoError(t, repo.TransferMessageAttachments(ctx, "force-transfer-foreign", "force-transfer-foreign-source", "force-transfer-foreign-destination", []string{"force-transfer-foreign-attachment"}))
	foreign, err := repo.GetMessageAttachment(ctx, "force-transfer-foreign-attachment")
	require.NoError(t, err)
	require.Equal(t, "force-transfer-foreign-destination", foreign.SessionID)
}

// @covers AC-TASKS-SAFE-FORCE-REMOVAL-004.1
func TestClaimForceRemovalRejectsSessionTransferRecoveryWithoutMutatingQueueState(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-transfer-queue-ws", Name: "Force"}))
	for _, taskID := range []string{"force-transfer-queue-task", "force-transfer-queue-foreign"} {
		require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: "force-transfer-queue-ws", Title: taskID}))
	}
	for _, session := range []*models.TaskSession{
		{ID: "force-transfer-queue-source", TaskID: "force-transfer-queue-task"},
		{ID: "force-transfer-queue-destination", TaskID: "force-transfer-queue-task"},
		{ID: "force-transfer-queue-foreign-source", TaskID: "force-transfer-queue-foreign"},
		{ID: "force-transfer-queue-foreign-destination", TaskID: "force-transfer-queue-foreign"},
	} {
		require.NoError(t, repo.CreateTaskSession(ctx, session))
	}

	queueRepo, err := messagequeue.NewSQLiteRepository(repo.db, repo.db)
	require.NoError(t, err)
	for _, entry := range []*messagequeue.QueuedMessage{
		{ID: "force-transfer-queue-source-entry", SessionID: "force-transfer-queue-source", TaskID: "force-transfer-queue-task", Content: "source", QueuedBy: messagequeue.QueuedByUser},
		{ID: "force-transfer-queue-destination-entry", SessionID: "force-transfer-queue-destination", TaskID: "force-transfer-queue-task", Content: "destination", QueuedBy: messagequeue.QueuedByUser},
		{ID: "force-transfer-queue-foreign-entry", SessionID: "force-transfer-queue-foreign-source", TaskID: "force-transfer-queue-foreign", Content: "foreign", QueuedBy: messagequeue.QueuedByUser},
	} {
		require.NoError(t, queueRepo.Insert(ctx, entry, messagequeue.DefaultMaxPerSession))
	}
	for _, pending := range []struct {
		sessionID string
		move      *messagequeue.PendingMove
	}{
		{"force-transfer-queue-source", &messagequeue.PendingMove{MoveID: "force-transfer-queue-source-move", TaskID: "force-transfer-queue-task", WorkflowID: "workflow", WorkflowStepID: "source"}},
		{"force-transfer-queue-destination", &messagequeue.PendingMove{MoveID: "force-transfer-queue-destination-move", TaskID: "force-transfer-queue-task", WorkflowID: "workflow", WorkflowStepID: "destination"}},
	} {
		require.NoError(t, queueRepo.SetPendingMove(ctx, pending.sessionID, pending.move))
	}

	task, err := repo.GetTask(ctx, "force-transfer-queue-task")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: task.ID, WorkspaceID: task.WorkspaceID, TaskGeneration: task.UpdatedAt, AdmissionGeneration: "admission", OperationID: "transfer-queue-operation", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)

	err = queueRepo.TransferSession(ctx, "force-transfer-queue-source", "force-transfer-queue-destination")
	require.ErrorIs(t, err, models.ErrForceRemovalTaskHeld)
	for _, want := range []struct {
		sessionID string
		entryID   string
		moveID    string
	}{
		{"force-transfer-queue-source", "force-transfer-queue-source-entry", "force-transfer-queue-source-move"},
		{"force-transfer-queue-destination", "force-transfer-queue-destination-entry", "force-transfer-queue-destination-move"},
	} {
		entries, listErr := queueRepo.ListBySession(ctx, want.sessionID)
		require.NoError(t, listErr)
		require.Len(t, entries, 1)
		require.Equal(t, want.entryID, entries[0].ID)
		pending, pendingErr := queueRepo.GetPendingMove(ctx, want.sessionID)
		require.NoError(t, pendingErr)
		require.NotNil(t, pending)
		require.Equal(t, want.moveID, pending.MoveID)
	}

	require.NoError(t, queueRepo.TransferSession(ctx, "force-transfer-queue-foreign-source", "force-transfer-queue-foreign-destination"))
	foreignEntries, err := queueRepo.ListBySession(ctx, "force-transfer-queue-foreign-destination")
	require.NoError(t, err)
	require.Len(t, foreignEntries, 1)
	require.Equal(t, "force-transfer-queue-foreign-entry", foreignEntries[0].ID)
}

// @covers AC-TASKS-SAFE-FORCE-REMOVAL-004.1
func TestClaimForceRemovalRejectsDeferredMoveApplicationWithoutConsumingReplay(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-deferred-apply-ws", Name: "Force"}))
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "force-deferred-apply-task", WorkspaceID: "force-deferred-apply-ws", Title: "Force"}))
	require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: "force-deferred-apply-session", TaskID: "force-deferred-apply-task", QueueIncarnationID: "force-deferred-incarnation"}))
	queueRepo, err := messagequeue.NewSQLiteRepository(repo.db, repo.db)
	require.NoError(t, err)
	move := &messagequeue.PendingMove{MoveID: "force-deferred-apply-move", SessionIncarnationID: "force-deferred-incarnation", TaskID: "force-deferred-apply-task", WorkflowID: "workflow", WorkflowStepID: "step", QueuedAt: time.Now().UTC()}
	require.NoError(t, queueRepo.SetPendingMove(ctx, "force-deferred-apply-session", move))

	task, err := repo.GetTask(ctx, "force-deferred-apply-task")
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: task.ID, WorkspaceID: task.WorkspaceID, TaskGeneration: task.UpdatedAt, AdmissionGeneration: "admission", OperationID: "deferred-apply-operation", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)

	staleReplay := *move
	staleReplay.SessionIncarnationID = "stale-incarnation"
	applied, err := repo.MarkDeferredMoveAppliedForSession(ctx, task.ID, move.MoveID, messagequeue.PendingMoveRecord{SessionID: "force-deferred-apply-session", Move: staleReplay})
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	require.False(t, applied)
	pending, err := queueRepo.GetPendingMove(ctx, "force-deferred-apply-session")
	require.NoError(t, err)
	require.NotNil(t, pending)
	require.Equal(t, move.MoveID, pending.MoveID)
	stored, err := repo.GetTask(ctx, task.ID)
	require.NoError(t, err)
	_, replayRecorded := stored.Metadata[models.MetaKeyAppliedDeferredMoves]
	require.False(t, replayRecorded)
}

// @covers AC-TASKS-SAFE-FORCE-REMOVAL-004.1
func TestClaimForceRemovalRejectsDeferredWorkflowAdmissionBeforeStaleReplayValidation(t *testing.T) {
	ctx := context.Background()
	repo := newRepoForHealTests(t)
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-deferred-admission-ws", Name: "Force"}))
	require.NoError(t, repo.CreateWorkflow(ctx, &models.Workflow{ID: "force-deferred-admission-workflow", WorkspaceID: "force-deferred-admission-ws", Name: "Workflow"}))
	seedCASWorkflowStep(t, repo, "force-deferred-admission-workflow", "force-deferred-admission-source", 0)
	seedCASWorkflowStep(t, repo, "force-deferred-admission-workflow", "force-deferred-admission-target", 1)
	task := &models.Task{ID: "force-deferred-admission-task", WorkspaceID: "force-deferred-admission-ws", WorkflowID: "force-deferred-admission-workflow", WorkflowStepID: "force-deferred-admission-source", Title: "Force", WIPAdmitted: true}
	require.NoError(t, repo.CreateTask(ctx, task))
	require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{ID: "force-deferred-admission-session", TaskID: task.ID, QueueIncarnationID: "force-deferred-admission-incarnation"}))
	queueRepo, err := messagequeue.NewSQLiteRepository(repo.db, repo.db)
	require.NoError(t, err)
	move := messagequeue.PendingMove{MoveID: "force-deferred-admission-move", SessionIncarnationID: "force-deferred-admission-incarnation", TaskID: task.ID, WorkflowID: task.WorkflowID, WorkflowStepID: "force-deferred-admission-target", QueuedAt: time.Now().UTC()}
	require.NoError(t, queueRepo.SetPendingMove(ctx, "force-deferred-admission-session", &move))

	stored, err := repo.GetTask(ctx, task.ID)
	require.NoError(t, err)
	_, _, err = repo.ClaimForceRemoval(ctx, &models.ForceRemovalClaim{TaskID: task.ID, WorkspaceID: task.WorkspaceID, TaskGeneration: stored.UpdatedAt, AdmissionGeneration: "admission", OperationID: "deferred-admission-operation", RequestDigest: "request", PreviewDigest: "preview"})
	require.NoError(t, err)
	staleReplay := move
	staleReplay.SessionIncarnationID = "stale-incarnation"

	admitted, applied, err := repo.UpdateTaskWithWorkflowStepAdmissionForDeferredMove(ctx, task, "force-deferred-admission-source", "force-deferred-admission-target", 0, messagequeue.PendingMoveRecord{SessionID: "force-deferred-admission-session", Move: staleReplay})
	require.ErrorIs(t, err, ErrForceRemovalTaskHeld)
	require.False(t, admitted)
	require.False(t, applied)
	pending, err := queueRepo.GetPendingMove(ctx, "force-deferred-admission-session")
	require.NoError(t, err)
	require.NotNil(t, pending)
	require.Equal(t, move.MoveID, pending.MoveID)
}
