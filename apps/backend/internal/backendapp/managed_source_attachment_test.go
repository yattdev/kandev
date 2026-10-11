package backendapp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	sqliterepo "github.com/kandev/kandev/internal/task/repository/sqlite"
	taskservice "github.com/kandev/kandev/internal/task/service"
)

func TestManagedSourceAttachmentMissingAndEmptyPaths(t *testing.T) {
	for _, route := range []string{"source", "branch"} {
		for _, saved := range []string{"missing", "empty"} {
			t.Run(route+"/"+saved, func(t *testing.T) {
				ctx := context.Background()
				checkout, root, primary := setupMaterializerScenario(t)
				repo := newMaterializerRepo(t)
				seedMaterializerTask(t, ctx, repo, checkout, root, primary)
				stale := ""
				if saved == "missing" {
					stale = filepath.Join(canonicalTempDir(t), "managed", "workspaces", "ws-1", "github", "acme", "notes")
				}
				if err := repo.CreateRepository(ctx, &models.Repository{ID: "notes", WorkspaceID: "ws-1", Name: "notes", SourceType: "provider", Provider: "github", ProviderOwner: "acme", ProviderName: "notes", LocalPath: stale, DefaultBranch: "main"}); err != nil {
					t.Fatal(err)
				}
				if err := repo.CreateTaskRepository(ctx, &models.TaskRepository{ID: "tr-notes", TaskID: "task-1", RepositoryID: "notes", BaseBranch: "main", Metadata: map[string]interface{}{}}); err != nil {
					t.Fatal(err)
				}
				cloner := &persistingAttachmentCloner{hostRepositoryClonerStub: hostRepositoryClonerStub{path: checkout}, repo: repo}
				mgr := newPersistingMaterializerWorktreeMgr(t, root, repo)
				mat := newWorkspaceSourceMaterializer(repo, mgr, nil, newTestLogger())
				mat.SetHostRepositoryCloner(cloner)
				if route == "source" {
					if _, err := mat.MaterializeWorkspaceSources(ctx, "task-1", &models.WorkspaceSourceBatch{TaskID: "task-1", Sources: []models.WorkspaceSource{{Repository: &models.TaskRepository{ID: "tr-notes", RepositoryID: "notes"}}}}); err != nil {
						t.Fatalf("source attach must materialize: %v", err)
					}
				} else {
					result, err := mat.branches.MaterializeBranch(ctx, "task-1", "tr-notes", taskservice.BranchMaterializationTarget{})
					if err != nil || result == nil || result.WorktreePath == "" {
						t.Fatalf("branch attach must materialize: result=%+v err=%v", result, err)
					}
				}
				if cloner.taskID != "task-1" || cloner.sessionID != "session-1" {
					t.Fatalf("authenticated clone scope=%q/%q", cloner.taskID, cloner.sessionID)
				}
				paths, err := mgr.GetAllByTaskID(ctx, "task-1")
				if err != nil || len(paths) == 0 {
					t.Fatalf("no registered worktree: %v", err)
				}
				found := false
				for _, wt := range paths {
					if wt.RepositoryID == "notes" {
						found = true
						if _, err := os.ReadFile(filepath.Join(wt.Path, "README.md")); err != nil {
							t.Fatal(err)
						}
					}
				}
				if !found {
					t.Fatal("attached notes worktree is absent")
				}
			})
		}
	}
}

type persistingAttachmentCloner struct {
	hostRepositoryClonerStub
	repo *sqliterepo.Repository
}

func (c *persistingAttachmentCloner) EnsureRepositoryClonedForSession(ctx context.Context, taskID, sessionID string, repository *models.Repository) (string, error) {
	path, err := c.hostRepositoryClonerStub.EnsureRepositoryClonedForSession(ctx, taskID, sessionID, repository)
	if err != nil {
		return "", err
	}
	repository.LocalPath = path
	return path, c.repo.UpdateRepository(ctx, repository)
}
