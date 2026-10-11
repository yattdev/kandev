package backendapp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kandev/kandev/internal/events/bus"
	"github.com/kandev/kandev/internal/orchestrator/executor"
	"github.com/kandev/kandev/internal/repoclone"
	"github.com/kandev/kandev/internal/task/models"
	taskservice "github.com/kandev/kandev/internal/task/service"
)

func TestManagedAttachmentServiceCloneAndCompensation(t *testing.T) {
	for _, route := range []string{"source", "branch"} {
		for _, scenario := range []string{"missing", "empty", "foreign", "local", "local-valid", "clone-failure"} {
			t.Run(route+"/"+scenario, func(t *testing.T) { exerciseManagedAttachmentService(t, route, scenario) })
		}
	}
}

func exerciseManagedAttachmentService(t *testing.T, route, scenario string) {
	t.Helper()
	ctx := context.Background()
	origin, root, primary := setupMaterializerScenario(t)
	repo := newMaterializerRepo(t)
	seedMaterializerTask(t, ctx, repo, origin, root, primary)
	log := newTestLogger()
	events := bus.NewMemoryEventBus(log)
	t.Cleanup(events.Close)
	svc := taskservice.NewService(taskservice.Repos{Workspaces: repo, Tasks: repo, TaskRepos: repo, WorkspaceFolders: repo, Workflows: repo, Turns: repo, Sessions: repo, RepoEntities: repo, TaskEnvironments: repo, BackgroundWork: repo, Messages: repo, GitSnapshots: repo, Executors: repo, Environments: repo, Usage: repo}, events, log, taskservice.RepositoryDiscoveryConfig{})
	cloner := repoclone.NewCloner(repoclone.Config{BasePath: canonicalTempDir(t)}, repoclone.ProtocolHTTPS, "", log)
	managed, err := cloner.WorkspaceProviderRepoPath("ws-1", "gitlab", "https://gitlab.example", "acme", "notes")
	if err != nil {
		t.Fatal(err)
	}
	saved := managed
	sourceType := "provider"
	if scenario == "empty" {
		saved = ""
	}
	if scenario == "foreign" || scenario == "local" {
		saved = filepath.Join(canonicalTempDir(t), "untouched")
	}
	if scenario == "local" || scenario == "local-valid" {
		sourceType = "local"
	}
	if scenario == "local-valid" {
		saved = origin
	}
	// A local Git transport keeps the real cloner and DB deterministic and offline.
	// This fixture tests materialization, not external provider authentication.
	if scenario == "clone-failure" {
		origin = filepath.Join(canonicalTempDir(t), "unavailable-origin")
	}
	notes := &models.Repository{ID: "notes", WorkspaceID: "ws-1", Name: "notes", SourceType: sourceType, Provider: "gitlab", ProviderHost: "https://gitlab.example", ProviderOwner: "acme", ProviderName: "notes", RemoteURL: origin, LocalPath: saved, DefaultBranch: "main"}
	if err := repo.CreateRepository(ctx, notes); err != nil {
		t.Fatal(err)
	}
	exc := executor.NewExecutor(nil, nil, log, executor.ExecutorConfig{})
	exc.SetRepoCloner(cloner, &repoLocalPathUpdater{svc: svc})
	mgr := newPersistingMaterializerWorktreeMgr(t, root, repo)
	mat := newWorkspaceSourceMaterializer(repo, mgr, nil, log)
	mat.SetHostRepositoryCloner(exc)
	svc.SetWorkspaceSourceMaterializer(mat)
	svc.SetBranchMaterializer(mat.branches)
	attach := func() error {
		if route == "source" {
			_, err := svc.AttachWorkspaceSources(ctx, taskservice.AttachWorkspaceSourcesRequest{TaskID: "task-1", Sources: []taskservice.WorkspaceSourceInput{{Kind: taskservice.WorkspaceSourceRepository, RepositoryID: "notes", BaseBranch: "main"}}})
			return err
		}
		result, err := svc.AddBranchToTask(ctx, taskservice.AddBranchToTaskRequest{TaskID: "task-1", RepositoryID: "notes", BaseBranch: "main"})
		if err == nil && (result == nil || result.WorktreePath == "") {
			t.Fatal("successful branch response has no worktree")
		}
		return err
	}
	err = attach()
	failure := scenario == "foreign" || scenario == "local" || scenario == "clone-failure"
	if failure && err == nil {
		t.Fatal("invalid source unexpectedly attached")
	}
	if !failure && err != nil {
		t.Fatalf("attach: %v", err)
	}
	rows, listErr := repo.ListTaskRepositories(ctx, "task-1")
	if listErr != nil {
		t.Fatal(listErr)
	}
	want := 2
	if failure {
		want = 1
	}
	if len(rows) != want {
		t.Fatalf("attachment rows=%d want %d", len(rows), want)
	}
	inventory, listErr := repo.ListTaskEnvironmentRepos(ctx, "env-1")
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(inventory) != want {
		t.Fatalf("inventory rows=%d want %d", len(inventory), want)
	}
	stored, readErr := repo.GetRepository(ctx, "notes")
	if readErr != nil {
		t.Fatal(readErr)
	}
	if failure {
		if stored.LocalPath != saved {
			t.Fatal("failed source redirected")
		}
		if scenario != "clone-failure" {
			if _, err := os.Lstat(managed); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid source cloned: %v", err)
			}
		}
		return
	}
	expectedPath := managed
	if scenario == "local-valid" {
		expectedPath = saved
	}
	if stored.LocalPath != expectedPath {
		t.Fatalf("persisted path=%q want %q", stored.LocalPath, expectedPath)
	}
	for _, row := range inventory {
		if row.RepositoryID == "notes" {
			if _, err := os.ReadFile(filepath.Join(row.WorktreePath, "README.md")); err != nil {
				t.Fatal(err)
			}
		}
	}
	if route == "source" {
		if err := attach(); err != nil {
			t.Fatalf("exact retry: %v", err)
		}
	}
}
