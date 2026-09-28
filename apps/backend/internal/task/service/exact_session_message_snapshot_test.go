package service

import (
	"context"
	"errors"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	taskrepo "github.com/kandev/kandev/internal/task/repository"
	sqliterepo "github.com/kandev/kandev/internal/task/repository/sqlite"
)

type recordingExactSessionMessageSnapshotRepository struct {
	*sqliterepo.Repository
	openRequest *models.ExactSessionMessageSnapshotRequest
	pageRequest *models.ExactSessionMessageSnapshotPageRequest
	pageErr     error
}

func (r *recordingExactSessionMessageSnapshotRepository) OpenExactSessionMessageSnapshot(_ context.Context, request models.ExactSessionMessageSnapshotRequest) (*models.ExactSessionMessageSnapshot, error) {
	r.openRequest = &request
	return &models.ExactSessionMessageSnapshot{Token: "snapshot"}, nil
}

func (r *recordingExactSessionMessageSnapshotRepository) PageExactSessionMessageSnapshot(_ context.Context, request models.ExactSessionMessageSnapshotPageRequest) ([]models.ExactSessionMessageSnapshotMessage, error) {
	r.pageRequest = &request
	if r.pageErr != nil {
		return nil, r.pageErr
	}
	return []models.ExactSessionMessageSnapshotMessage{{ID: "message"}}, nil
}

// taskRepositoryWithoutExactSessionMessages preserves the normal task reader
// but intentionally withholds the private exact-message capability.
type taskRepositoryWithoutExactSessionMessages struct{ taskrepo.TaskRepository }

func TestServiceExactSessionMessageSnapshotDerivesStoredSessionFences(t *testing.T) {
	svc, _, repo := createTestService(t)
	ctx := context.Background()
	seedExactSessionMessageSnapshotTask(t, repo, "workspace-a", "task-a", "session-a")
	stored, err := repo.GetTaskSession(ctx, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	recording := &recordingExactSessionMessageSnapshotRepository{Repository: repo}
	svc.tasks = recording

	if _, err := svc.OpenExactSessionMessageSnapshot(ctx, "installation-a", "workspace-a", "task-a", "session-a"); err != nil {
		t.Fatalf("OpenExactSessionMessageSnapshot: %v", err)
	}
	want := models.ExactSessionMessageSnapshotRequest{
		InstallationID: "installation-a", WorkspaceID: "workspace-a", TaskID: "task-a", SessionID: "session-a",
		QueueIncarnationID: stored.QueueIncarnationID, RouteGeneration: stored.RouteGeneration, SessionResourceVersion: stored.ResourceVersion,
	}
	if recording.openRequest == nil || *recording.openRequest != want {
		t.Fatalf("open request = %#v, want %#v", recording.openRequest, want)
	}
	rows, err := svc.PageExactSessionMessageSnapshot(ctx, "installation-a", "workspace-a", "task-a", "session-a", "snapshot", 2, 3)
	if err != nil || len(rows) != 1 {
		t.Fatalf("PageExactSessionMessageSnapshot = %#v, %v", rows, err)
	}
	wantPage := models.ExactSessionMessageSnapshotPageRequest{ExactSessionMessageSnapshotRequest: want, Token: "snapshot", Offset: 2, Limit: 3}
	if recording.pageRequest == nil || *recording.pageRequest != wantPage {
		t.Fatalf("page request = %#v, want %#v", recording.pageRequest, wantPage)
	}
}

func TestServiceExactSessionMessageSnapshotFailsClosed(t *testing.T) {
	svc, _, repo := createTestService(t)
	ctx := context.Background()
	seedExactSessionMessageSnapshotTask(t, repo, "workspace-a", "task-a", "session-a")
	seedExactSessionMessageSnapshotTask(t, repo, "workspace-b", "task-b", "session-b")
	recording := &recordingExactSessionMessageSnapshotRepository{Repository: repo}
	svc.tasks = recording

	if _, err := svc.OpenExactSessionMessageSnapshot(ctx, "installation-a", "workspace-a", "task-a", "session-b"); !errors.Is(err, taskrepo.ErrExactSessionMessageSnapshotUnavailable) {
		t.Fatalf("mismatched session error = %v, want unavailable", err)
	}
	if recording.openRequest != nil {
		t.Fatalf("mismatched session opened repository request %#v", recording.openRequest)
	}
	if _, err := svc.PageExactSessionMessageSnapshot(ctx, "installation-a", "workspace-b", "task-a", "session-a", "snapshot", 0, 1); !errors.Is(err, taskrepo.ErrExactSessionMessageSnapshotUnavailable) {
		t.Fatalf("foreign workspace error = %v, want unavailable", err)
	}
	if recording.pageRequest != nil {
		t.Fatalf("foreign workspace paged repository request %#v", recording.pageRequest)
	}

	recording.pageErr = taskrepo.ErrExactSessionMessageSnapshotUnavailable
	if _, err := svc.PageExactSessionMessageSnapshot(ctx, "installation-a", "workspace-a", "task-a", "session-a", "expired", 0, 1); !errors.Is(err, taskrepo.ErrExactSessionMessageSnapshotUnavailable) {
		t.Fatalf("expired snapshot error = %v, want unavailable", err)
	}
	if recording.pageRequest == nil || recording.pageRequest.Token != "expired" {
		t.Fatalf("expired request = %#v, want authoritative repository read", recording.pageRequest)
	}

	svc.tasks = taskRepositoryWithoutExactSessionMessages{TaskRepository: repo}
	if _, err := svc.OpenExactSessionMessageSnapshot(ctx, "installation-a", "workspace-a", "task-a", "session-a"); !errors.Is(err, taskrepo.ErrExactSessionMessageSnapshotUnavailable) {
		t.Fatalf("unsupported open error = %v, want unavailable", err)
	}
	if _, err := svc.PageExactSessionMessageSnapshot(ctx, "installation-a", "workspace-a", "task-a", "session-a", "snapshot", 0, 1); !errors.Is(err, taskrepo.ErrExactSessionMessageSnapshotUnavailable) {
		t.Fatalf("unsupported page error = %v, want unavailable", err)
	}
}

func seedExactSessionMessageSnapshotTask(t *testing.T, repo *sqliterepo.Repository, workspaceID, taskID, sessionID string) {
	t.Helper()
	ctx := context.Background()
	if err := repo.CreateWorkspace(ctx, &models.Workspace{ID: workspaceID, Name: workspaceID}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateTask(ctx, &models.Task{ID: taskID, WorkspaceID: workspaceID, Title: taskID}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateTaskSession(ctx, &models.TaskSession{ID: sessionID, TaskID: taskID, QueueIncarnationID: "trusted-" + sessionID, RouteGeneration: 17, State: models.TaskSessionStateCreated}); err != nil {
		t.Fatal(err)
	}
}
