package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/task/models"
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

func (*cascadeMutationRolledBack) ArchiveTaskIfActiveWithVacatedStep(context.Context, string, string) (string, bool, error) {
	return "", false, errors.New("archive transaction rolled back")
}

func (*cascadeMutationRolledBack) DeleteTaskWithVacatedStep(context.Context, string) (string, error) {
	return "", errors.New("delete transaction rolled back")
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
	_, err = handoff.ArchiveAutoTask(ctx, candidate)
	if err == nil {
		t.Fatal("expected lost autoarchive commit response")
	}
	current, err := repo.GetTask(ctx, id)
	if err != nil || current == nil || current.ArchivedAt == nil {
		t.Fatalf("autoarchived task = %+v error = %v", current, err)
	}
	var state string
	if err := repo.DB().QueryRowContext(ctx,
		`SELECT state FROM task_resource_cleanup_jobs WHERE task_id = ? AND trigger = ?`, id, "cascade_archive",
	).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state == "cancelled" || state == "prepared" {
		t.Fatalf("committed autoarchive cleanup state = %s", state)
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
