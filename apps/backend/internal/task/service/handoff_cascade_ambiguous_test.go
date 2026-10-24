package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	orchmodels "github.com/kandev/kandev/internal/office/models"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository"
	sqliterepo "github.com/kandev/kandev/internal/task/repository/sqlite"
	wfmodels "github.com/kandev/kandev/internal/workflow/models"
	workflowrepo "github.com/kandev/kandev/internal/workflow/repository"
)

type cascadeCommitResponseLost struct{ *sqliterepo.Repository }

type cascadeMutationRolledBack struct{ *sqliterepo.Repository }

type cascadeOutcomeReadUnavailable struct {
	*sqliterepo.Repository
	denyReads bool
}

func (r *cascadeOutcomeReadUnavailable) GetTask(ctx context.Context, id string) (*models.Task, error) {
	if r.denyReads {
		return nil, errors.New("authoritative task read unavailable")
	}
	return r.Repository.GetTask(ctx, id)
}

func (r *cascadeOutcomeReadUnavailable) ArchiveTaskIfActiveWithVacatedStep(ctx context.Context, id, cascadeID string) (string, bool, error) {
	step, changed, err := r.Repository.ArchiveTaskIfActiveWithVacatedStep(ctx, id, cascadeID)
	if err != nil || !changed {
		return step, changed, err
	}
	r.denyReads = true
	return step, false, errors.New("archive commit response lost")
}

func (r *cascadeOutcomeReadUnavailable) DeleteTaskWithVacatedStep(ctx context.Context, id string) (string, error) {
	step, err := r.Repository.DeleteTaskWithVacatedStep(ctx, id)
	if err != nil {
		return step, err
	}
	r.denyReads = true
	return step, errors.New("delete commit response lost")
}

func (r *cascadeOutcomeReadUnavailable) ArchiveTaskIfAutoArchiveEligible(ctx context.Context, id string, updatedAt time.Time, cascadeID string) (bool, error) {
	changed, err := r.Repository.ArchiveTaskIfAutoArchiveEligible(ctx, id, updatedAt, cascadeID)
	if err != nil || !changed {
		return changed, err
	}
	r.denyReads = true
	return false, errors.New("autoarchive commit response lost")
}

func (*cascadeMutationRolledBack) ArchiveTaskIfActiveWithVacatedStep(context.Context, string, string) (string, bool, error) {
	return "", false, errors.New("archive transaction rolled back")
}

func (*cascadeMutationRolledBack) DeleteTaskWithVacatedStep(context.Context, string) (string, error) {
	return "", errors.New("delete transaction rolled back")
}

func (*cascadeMutationRolledBack) ArchiveTaskIfAutoArchiveEligible(context.Context, string, time.Time, string) (bool, error) {
	return false, errors.New("autoarchive transaction rolled back")
}

func (r *cascadeCommitResponseLost) ArchiveTaskIfActiveWithVacatedStep(ctx context.Context, id, cascadeID string) (string, bool, error) {
	step, changed, err := r.Repository.ArchiveTaskIfActiveWithVacatedStep(ctx, id, cascadeID)
	if err != nil || !changed {
		return step, changed, err
	}
	return step, false, errors.New("archive commit response lost")
}

func (r *cascadeCommitResponseLost) DeleteTaskWithVacatedStep(ctx context.Context, id string) (string, error) {
	step, err := r.Repository.DeleteTaskWithVacatedStep(ctx, id)
	if err != nil {
		return step, err
	}
	return step, errors.New("delete commit response lost")
}

func (r *cascadeCommitResponseLost) ArchiveTaskIfAutoArchiveEligible(ctx context.Context, id string, updatedAt time.Time, cascadeID string) (bool, error) {
	changed, err := r.Repository.ArchiveTaskIfAutoArchiveEligible(ctx, id, updatedAt, cascadeID)
	if err != nil || !changed {
		return changed, err
	}
	return false, errors.New("autoarchive commit response lost")
}

func TestAutoarchiveCommittedResponseLossRetainsCleanup(t *testing.T) {
	ctx := context.Background()
	svc, _, repo := createTestService(t)
	svc.StopTaskResourceCleanupWorker()
	id := "ambiguous-autoarchive"
	seedCleanupTaskAndSession(t, repo, id, "session-autoarchive")
	db := sqlx.NewDb(repo.DB(), "sqlite3")
	steps, err := workflowrepo.NewWithDB(db, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := steps.CreateStep(ctx, &wfmodels.WorkflowStep{
		ID: "step", WorkflowID: "wf-" + id, Name: "Done", AutoArchiveAfterHours: 1,
	}); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-48 * time.Hour)
	if _, err := repo.DB().ExecContext(ctx, `UPDATE tasks SET updated_at = ? WHERE id = ?`, old, id); err != nil {
		t.Fatal(err)
	}
	candidate, err := repo.GetTask(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	handoff := NewHandoffService(&cascadeCommitResponseLost{repo}, nil, nil, nil, nil, nil)
	handoff.SetTaskResourceCleaner(svc)
	runCanceller := &recordingRunCanceller{}
	handoff.SetRunCanceller(runCanceller)
	_, err = handoff.ArchiveAutoTask(ctx, candidate)
	if err == nil {
		t.Fatal("expected lost autoarchive commit response")
	}
	if len(runCanceller.calls) != 1 || runCanceller.calls[0] != id {
		t.Fatalf("committed autoarchive run cancellation = %v, want [%s]", runCanceller.calls, id)
	}
	current, err := repo.GetTask(ctx, id)
	if err != nil || current == nil || current.ArchivedAt == nil {
		t.Fatalf("autoarchived task = %+v error = %v", current, err)
	}
	var state, operationID string
	if err := repo.DB().QueryRowContext(ctx,
		`SELECT state, operation_id FROM task_resource_cleanup_jobs WHERE task_id = ? AND trigger = ?`, id, "cascade_archive",
	).Scan(&state, &operationID); err != nil {
		t.Fatal(err)
	}
	if state == "cancelled" || state == "prepared" {
		t.Fatalf("committed autoarchive cleanup state = %s", state)
	}
	if committed, known, err := svc.ResolveCascadeTaskResourceCleanupAfterMutationError(ctx, operationID); err != nil || !known || !committed {
		t.Fatalf("repeat autoarchive outcome = (%v, %v, %v)", committed, known, err)
	}
	if len(runCanceller.calls) != 1 {
		t.Fatalf("repeat outcome cancelled runs %d times", len(runCanceller.calls))
	}
}

func TestCascadeCommittedMutationResponseLossRetainsCleanup(t *testing.T) {
	for _, trigger := range []string{"cascade_archive", "cascade_delete"} {
		t.Run(trigger, func(t *testing.T) {
			ctx := context.Background()
			svc, _, repo := createTestService(t)
			svc.StopTaskResourceCleanupWorker()
			id := "ambiguous-" + trigger
			seedCleanupTaskAndSession(t, repo, id, "session-"+trigger)
			handoff := NewHandoffService(&cascadeCommitResponseLost{repo}, nil, nil, nil, nil, nil)
			handoff.SetTaskResourceCleaner(svc)
			var err error
			if trigger == "cascade_archive" {
				_, err = handoff.ArchiveTaskTree(ctx, id, false)
			} else {
				_, err = handoff.DeleteTaskTree(ctx, id, false)
			}
			if err == nil {
				t.Fatal("expected lost commit response")
			}
			var state, operationID string
			if err := repo.DB().QueryRowContext(ctx,
				`SELECT state, operation_id FROM task_resource_cleanup_jobs WHERE task_id = ? AND trigger = ?`, id, trigger,
			).Scan(&state, &operationID); err != nil {
				t.Fatal(err)
			}
			if state == "cancelled" || state == "prepared" {
				t.Fatalf("committed %s cleanup state = %s, want activated", trigger, state)
			}
			if committed, known, err := svc.ResolveCascadeTaskResourceCleanupAfterMutationError(ctx, operationID); err != nil || !known || !committed {
				t.Fatalf("repeated committed resolution = (%v, %v, %v)", committed, known, err)
			}
			if err := svc.ResumeTaskResourceCleanupJobs(ctx); err != nil {
				t.Fatalf("resume committed cleanup: %v", err)
			}
		})
	}
}

func TestCascadeRolledBackMutationCancelsOnlyItsCleanup(t *testing.T) {
	for _, trigger := range []string{"cascade_archive", "cascade_delete"} {
		t.Run(trigger, func(t *testing.T) {
			ctx := context.Background()
			svc, _, repo := createTestService(t)
			svc.StopTaskResourceCleanupWorker()
			id := "rolled-back-" + trigger
			seedCleanupTaskAndSession(t, repo, id, "session-"+trigger)
			handoff := NewHandoffService(&cascadeMutationRolledBack{repo}, nil, nil, nil, nil, nil)
			handoff.SetTaskResourceCleaner(svc)
			var err error
			if trigger == "cascade_archive" {
				_, err = handoff.ArchiveTaskTree(ctx, id, false)
			} else {
				_, err = handoff.DeleteTaskTree(ctx, id, false)
			}
			if err == nil {
				t.Fatal("expected rolled-back mutation error")
			}
			task, err := repo.GetTask(ctx, id)
			if err != nil || task == nil || task.ArchivedAt != nil {
				t.Fatalf("rolled-back task = %+v, error = %v", task, err)
			}
			var state, operationID string
			if err := repo.DB().QueryRowContext(ctx,
				`SELECT state, operation_id FROM task_resource_cleanup_jobs WHERE task_id = ? AND trigger = ?`, id, trigger,
			).Scan(&state, &operationID); err != nil {
				t.Fatal(err)
			}
			if state != "cancelled" {
				t.Fatalf("rolled-back cleanup state = %s, want cancelled", state)
			}
			if committed, known, err := svc.ResolveCascadeTaskResourceCleanupAfterMutationError(ctx, operationID); err != nil || !known || committed {
				t.Fatalf("repeated rollback resolution = (%v, %v, %v)", committed, known, err)
			}
		})
	}
}

func TestCascadeUnknownOutcomeKeepsPreparedCleanupForReplay(t *testing.T) {
	for _, trigger := range []string{"cascade_archive", "cascade_delete"} {
		t.Run(trigger, func(t *testing.T) {
			ctx := context.Background()
			svc, _, repo := createTestService(t)
			svc.StopTaskResourceCleanupWorker()
			id := "unknown-" + trigger
			seedCleanupTaskAndSession(t, repo, id, "session-"+trigger)
			wrapped := &cascadeOutcomeReadUnavailable{Repository: repo}
			svc.tasks = wrapped
			handoff := NewHandoffService(wrapped, nil, nil, nil, nil, nil)
			handoff.SetTaskResourceCleaner(svc)
			var err error
			if trigger == "cascade_archive" {
				_, err = handoff.ArchiveTaskTree(ctx, id, false)
			} else {
				_, err = handoff.DeleteTaskTree(ctx, id, false)
			}
			if err == nil {
				t.Fatal("expected unknown mutation outcome")
			}
			var state string
			readState := func() string {
				t.Helper()
				if err := repo.DB().QueryRowContext(ctx,
					`SELECT state FROM task_resource_cleanup_jobs WHERE task_id = ? AND trigger = ?`, id, trigger,
				).Scan(&state); err != nil {
					t.Fatal(err)
				}
				return state
			}
			if got := readState(); got != "prepared" {
				t.Fatalf("unknown outcome job = %s, want prepared", got)
			}
			wrapped.denyReads = false
			if err := svc.ResumeTaskResourceCleanupJobs(ctx); err != nil {
				t.Fatalf("replay verified commit: %v", err)
			}
			if got := readState(); got == "prepared" || got == "cancelled" {
				t.Fatalf("replayed committed job = %s", got)
			}
		})
	}
}

func TestCascadeHousekeepingAutoarchiveSuccessCancelsRunControl(t *testing.T) {
	tasks := newFakeTaskRepo()
	tasks.addTask("candidate", "", "ws-1")
	coordinator := &recordingCleanupCoordinator{}
	canceller := &recordingRunCanceller{}
	handoff := NewHandoffService(newCascadeRepo(tasks), nil, nil, nil, nil, nil)
	handoff.SetTaskResourceCleaner(coordinator)
	handoff.SetRunCanceller(canceller)
	if _, err := handoff.ArchiveAutoTask(context.Background(), tasks.tasks["candidate"]); err != nil {
		t.Fatal(err)
	}
	if len(canceller.calls) != 1 || canceller.calls[0] != "candidate" {
		t.Fatalf("success cancellation=%v", canceller.calls)
	}
}

func TestCascadeHousekeepingCommittedArchiveResponseLossReleasesGroupControl(t *testing.T) {
	for _, lost := range []bool{false, true} {
		name := "success"
		if lost {
			name = "response_lost"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			svc, _, repo := createTestService(t)
			svc.StopTaskResourceCleanupWorker()
			id := "review-group-" + name
			seedCleanupTaskAndSession(t, repo, id, "session-"+name)
			groups := newCascadeWSGroupRepo()
			groups.groups["g1"] = &orchmodels.WorkspaceGroup{ID: "g1", WorkspaceID: "ws-" + id, OwnerTaskID: id, MaterializedKind: orchmodels.WorkspaceGroupKindSingleRepo, CleanupStatus: orchmodels.WorkspaceCleanupStatusActive}
			groups.members["g1"] = map[string]string{id: orchmodels.WorkspaceMemberRoleOwner}
			var tasksRepo repository.TaskRepository = repo
			if lost {
				tasksRepo = &cascadeCommitResponseLost{repo}
			}
			handoff := NewHandoffService(tasksRepo, nil, nil, nil, groups, nil)
			handoff.SetTaskResourceCleaner(svc)
			publisher := &fakeEventPublisher{}
			handoff.SetTaskEventPublisher(publisher)
			_, err := handoff.ArchiveTaskTree(ctx, id, false)
			if !lost && err != nil {
				t.Fatal(err)
			}
			if lost && err == nil {
				t.Fatal("expected lost response")
			}
			task, getErr := repo.GetTask(ctx, id)
			if getErr != nil || task == nil || task.ArchivedAt == nil {
				t.Fatalf("archived task=%+v err=%v", task, getErr)
			}
			if len(groups.releaseCalls) != 1 {
				t.Fatalf("committed archive membership release calls=%v, want one", groups.releaseCalls)
			}
			if len(publisher.updated) != 1 || publisher.updated[0] != id {
				t.Fatalf("archive event count/identity = %v", publisher.updated)
			}
			if lost {
				assertRepeatedCascadeResolution(t, ctx, svc, repo, id, "cascade_archive")
				if len(groups.releaseCalls) != 1 || len(publisher.updated) != 1 {
					t.Fatalf("replay repeated archive effects: releases=%v events=%v", groups.releaseCalls, publisher.updated)
				}
			}
		})
	}
}

func TestCascadeHousekeepingCommittedDeleteResponseLossEvaluatesReleasedGroupControl(t *testing.T) {
	for _, lost := range []bool{false, true} {
		name := "success"
		if lost {
			name = "response_lost"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			svc, _, repo := createTestService(t)
			svc.StopTaskResourceCleanupWorker()
			id := "review-delete-group-" + name
			seedCleanupTaskAndSession(t, repo, id, "session-"+name)
			groups := newCascadeWSGroupRepo()
			groups.groups["g1"] = &orchmodels.WorkspaceGroup{ID: "g1", WorkspaceID: "ws-" + id, OwnerTaskID: id, MaterializedKind: orchmodels.WorkspaceGroupKindSingleRepo, CleanupStatus: orchmodels.WorkspaceCleanupStatusActive, OwnedByKandev: true, CleanupPolicy: orchmodels.WorkspaceCleanupPolicyDeleteWhenLastMemberArchivedOrDel}
			groups.members["g1"] = map[string]string{id: orchmodels.WorkspaceMemberRoleOwner}
			var tasksRepo repository.TaskRepository = repo
			if lost {
				tasksRepo = &cascadeCommitResponseLost{repo}
			}
			handoff := NewHandoffService(tasksRepo, nil, nil, nil, groups, nil)
			handoff.SetTaskResourceCleaner(svc)
			publisher := &fakeEventPublisher{}
			handoff.SetTaskEventPublisher(publisher)
			_, err := handoff.DeleteTaskTree(ctx, id, false)
			if !lost && err != nil {
				t.Fatal(err)
			}
			if lost && err == nil {
				t.Fatal("expected lost response")
			}
			task, getErr := repo.GetTask(ctx, id)
			if getErr == nil && task != nil {
				t.Fatalf("task still exists: %+v", task)
			}
			if len(groups.releaseCalls) != 1 {
				t.Fatalf("group releases=%v", groups.releaseCalls)
			}
			if groups.cleanupStatuses["g1"] != orchmodels.WorkspaceCleanupStatusPending {
				t.Fatalf("group cleanup status=%q, want pending", groups.cleanupStatuses["g1"])
			}
			if len(publisher.deleted) != 1 || publisher.deleted[0] != id {
				t.Fatalf("delete event count/identity = %v", publisher.deleted)
			}
			if lost {
				assertRepeatedCascadeResolution(t, ctx, svc, repo, id, "cascade_delete")
				if len(groups.releaseCalls) != 1 || groups.cleanupStatuses["g1"] != orchmodels.WorkspaceCleanupStatusPending || len(publisher.deleted) != 1 {
					t.Fatalf("replay changed delete effects: releases=%v status=%q events=%v", groups.releaseCalls, groups.cleanupStatuses["g1"], publisher.deleted)
				}
			}
		})
	}
}

func TestCascadeHousekeepingAutoarchiveRollbackAndUnknownDoNotCancelRuns(t *testing.T) {
	for _, mode := range []string{"rollback", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			svc, _, repo := createTestService(t)
			svc.StopTaskResourceCleanupWorker()
			id := "autoarchive-" + mode
			seedCleanupTaskAndSession(t, repo, id, "session-"+mode)
			db := sqlx.NewDb(repo.DB(), "sqlite3")
			steps, err := workflowrepo.NewWithDB(db, db, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := steps.CreateStep(ctx, &wfmodels.WorkflowStep{
				ID: "step", WorkflowID: "wf-" + id, Name: "Done", AutoArchiveAfterHours: 1,
			}); err != nil {
				t.Fatal(err)
			}
			old := time.Now().UTC().Add(-48 * time.Hour)
			if _, err := repo.DB().ExecContext(ctx, `UPDATE tasks SET updated_at = ? WHERE id = ?`, old, id); err != nil {
				t.Fatal(err)
			}
			candidate, err := repo.GetTask(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			var tasksRepo repository.TaskRepository = &cascadeMutationRolledBack{repo}
			if mode == "unknown" {
				wrapped := &cascadeOutcomeReadUnavailable{Repository: repo}
				tasksRepo = wrapped
				svc.tasks = wrapped
			}
			handoff := NewHandoffService(tasksRepo, nil, nil, nil, nil, nil)
			handoff.SetTaskResourceCleaner(svc)
			canceller := &recordingRunCanceller{}
			handoff.SetRunCanceller(canceller)
			if _, err := handoff.ArchiveAutoTask(ctx, candidate); err == nil {
				t.Fatal("expected mutation error")
			}
			if len(canceller.calls) != 0 {
				t.Fatalf("unverified autoarchive cancelled runs: %v", canceller.calls)
			}
			var state string
			if err := repo.DB().QueryRowContext(ctx,
				`SELECT state FROM task_resource_cleanup_jobs WHERE task_id = ? AND trigger = 'cascade_archive'`, id,
			).Scan(&state); err != nil {
				t.Fatal(err)
			}
			want := "cancelled"
			if mode == "unknown" {
				want = "prepared"
			}
			if state != want {
				t.Fatalf("cleanup job state = %q, want %q", state, want)
			}
		})
	}
}

func TestCascadeHousekeepingRollbackAndUnknownDoNotEvaluateGroups(t *testing.T) {
	for _, trigger := range []string{"archive", "delete"} {
		for _, mode := range []string{"rollback", "unknown"} {
			t.Run(trigger+"/"+mode, func(t *testing.T) {
				testCascadeHousekeepingUncommittedGroup(t, trigger, mode)
			})
		}
	}
}

func testCascadeHousekeepingUncommittedGroup(t *testing.T, trigger, mode string) {
	t.Helper()
	ctx := context.Background()
	svc, _, repo := createTestService(t)
	svc.StopTaskResourceCleanupWorker()
	id := "group-" + trigger + "-" + mode
	seedCleanupTaskAndSession(t, repo, id, "session-"+id)
	groups := newCascadeWSGroupRepo()
	groups.groups["g1"] = &orchmodels.WorkspaceGroup{
		ID: "g1", WorkspaceID: "ws-" + id, OwnerTaskID: id,
		MaterializedKind: orchmodels.WorkspaceGroupKindSingleRepo,
		CleanupStatus:    orchmodels.WorkspaceCleanupStatusActive,
		OwnedByKandev:    true,
		CleanupPolicy:    orchmodels.WorkspaceCleanupPolicyDeleteWhenLastMemberArchivedOrDel,
	}
	groups.members["g1"] = map[string]string{id: orchmodels.WorkspaceMemberRoleOwner}
	var tasksRepo repository.TaskRepository = &cascadeMutationRolledBack{repo}
	if mode == "unknown" {
		wrapped := &cascadeOutcomeReadUnavailable{Repository: repo}
		tasksRepo = wrapped
		svc.tasks = wrapped
	}
	handoff := NewHandoffService(tasksRepo, nil, nil, nil, groups, nil)
	handoff.SetTaskResourceCleaner(svc)
	var err error
	if trigger == "archive" {
		_, err = handoff.ArchiveTaskTree(ctx, id, false)
	} else {
		_, err = handoff.DeleteTaskTree(ctx, id, false)
	}
	if err == nil {
		t.Fatal("expected mutation error")
	}
	if groups.cleanupStatuses["g1"] == orchmodels.WorkspaceCleanupStatusPending {
		t.Fatal("unverified mutation scheduled group cleanup")
	}
	if trigger == "archive" && len(groups.releaseCalls) != 0 {
		t.Fatalf("unverified archive released membership: %v", groups.releaseCalls)
	}
	var state string
	if err := repo.DB().QueryRowContext(ctx,
		`SELECT state FROM task_resource_cleanup_jobs WHERE task_id = ? AND trigger = ?`, id, "cascade_"+trigger,
	).Scan(&state); err != nil {
		t.Fatal(err)
	}
	want := "cancelled"
	if mode == "unknown" {
		want = "prepared"
	}
	if state != want {
		t.Fatalf("cleanup job state = %q, want %q", state, want)
	}
}

func assertRepeatedCascadeResolution(t *testing.T, ctx context.Context, svc *Service, repo *sqliterepo.Repository, taskID, trigger string) {
	t.Helper()
	var operationID string
	if err := repo.DB().QueryRowContext(ctx,
		`SELECT operation_id FROM task_resource_cleanup_jobs WHERE task_id = ? AND trigger = ?`, taskID, trigger,
	).Scan(&operationID); err != nil {
		t.Fatal(err)
	}
	if committed, known, err := svc.ResolveCascadeTaskResourceCleanupAfterMutationError(ctx, operationID); err != nil || !known || !committed {
		t.Fatalf("repeated cascade resolution = (%v, %v, %v)", committed, known, err)
	}
	if err := svc.ResumeTaskResourceCleanupJobs(ctx); err != nil {
		t.Fatalf("resume committed cascade cleanup: %v", err)
	}
}

func TestCascadeHousekeepingPartialCommittedArchiveRetainsRetryIdentity(t *testing.T) {
	ctx := context.Background()
	svc, _, repo := createTestService(t)
	svc.StopTaskResourceCleanupWorker()
	seedCleanupTaskAndSession(t, repo, "partial-root", "partial-session")
	if err := repo.CreateTask(ctx, &models.Task{
		ID: "partial-child", ParentID: "partial-root", WorkspaceID: "ws-partial-root",
		WorkflowID: "wf-partial-root", WorkflowStepID: "step", Title: "Child",
	}); err != nil {
		t.Fatal(err)
	}
	handoff := NewHandoffService(&cascadeCommitResponseLost{repo}, nil, nil, nil, nil, nil)
	handoff.SetTaskResourceCleaner(svc)
	if _, err := handoff.ArchiveTaskTree(ctx, "partial-root", true); err == nil {
		t.Fatal("expected first committed child archive response loss")
	}
	child, err := repo.GetTask(ctx, "partial-child")
	if err != nil || child == nil || child.ArchivedAt == nil || child.ArchivedByCascadeID == "" {
		t.Fatalf("committed child=%+v error=%v", child, err)
	}
	root, err := repo.GetTask(ctx, "partial-root")
	if err != nil || root == nil || root.ArchivedAt != nil {
		t.Fatalf("unprocessed root=%+v error=%v", root, err)
	}
	if root.Metadata[partialArchiveCascadeMetadataKey] != child.ArchivedByCascadeID {
		t.Fatalf("partial marker=%v, want %s", root.Metadata[partialArchiveCascadeMetadataKey], child.ArchivedByCascadeID)
	}
	retry := NewHandoffService(repo, nil, nil, nil, nil, nil)
	retry.SetTaskResourceCleaner(svc)
	if _, err := retry.ArchiveTaskTree(ctx, "partial-root", true); err != nil {
		t.Fatalf("retry partial cascade: %v", err)
	}
	root, err = repo.GetTask(ctx, "partial-root")
	if err != nil || root == nil || root.ArchivedAt == nil || root.ArchivedByCascadeID != child.ArchivedByCascadeID {
		t.Fatalf("retried root=%+v error=%v", root, err)
	}
	if root.Metadata[partialArchiveCascadeMetadataKey] != nil {
		t.Fatalf("completed cascade retained partial marker: %v", root.Metadata)
	}
}
