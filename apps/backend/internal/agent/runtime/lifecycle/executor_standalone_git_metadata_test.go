package lifecycle

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kandev/kandev/internal/worktree"
)

func TestStandaloneExecutorCreatesTaskScopedGitMetadataBeforeAgentctl(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	runContainerGit(t, "", "init", "-b", "main", repo)
	runContainerGit(t, repo, "config", "user.email", "test@example.com")
	runContainerGit(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "file"), []byte("initial"), 0o600); err != nil {
		t.Fatal(err)
	}
	runContainerGit(t, repo, "add", "file")
	runContainerGit(t, repo, "commit", "-m", "initial")
	checkout := filepath.Join(t.TempDir(), "checkout")
	runContainerGit(t, repo, "worktree", "add", "-b", "task", checkout)
	projection, err := worktree.ResolveGitMetadataForRepository(checkout, repo)
	if err != nil {
		t.Fatal(err)
	}
	runContainerGit(t, "", "init", "-b", "main", filepath.Join(checkout, "nested-clone"))
	sharedSiblingRef := filepath.Join(projection.SharedCommonDir, "refs", "heads", "main")
	siblingBefore, err := os.ReadFile(sharedSiblingRef)
	if err != nil {
		t.Fatal(err)
	}
	control := newStandaloneControlServer(t, true)
	request := &ExecutorCreateRequest{
		InstanceID:             "instance-1",
		TaskID:                 "task-1",
		SessionID:              "session-1",
		WorkspacePath:          checkout,
		GitMetadataProjections: []*worktree.GitMetadataProjection{projection},
		PreparedGitCheckouts:   []string{checkout},
	}
	request.GitMetadataProjections = nil
	if _, err := control.executor(t).CreateInstance(context.Background(), request); err == nil {
		t.Fatal("standalone launch accepted a linked checkout without its trusted projection")
	}
	request.GitMetadataProjections = []*worktree.GitMetadataProjection{projection}
	request.PreparedGitCheckouts = []string{checkout, checkout}
	if _, err := control.executor(t).CreateInstance(context.Background(), request); err == nil {
		t.Fatal("standalone launch accepted a duplicate prepared checkout binding")
	}
	request.PreparedGitCheckouts = []string{checkout}
	untrusted, err := worktree.ResolveGitMetadata(checkout)
	if err != nil {
		t.Fatal(err)
	}
	request.GitMetadataProjections = []*worktree.GitMetadataProjection{untrusted}
	if _, err := control.executor(t).CreateInstance(context.Background(), request); err == nil {
		t.Fatal("standalone launch accepted a projection without a trusted repository binding")
	}
	request.GitMetadataProjections = []*worktree.GitMetadataProjection{projection}
	request.WorkspacePath = repo
	if _, err := control.executor(t).CreateInstance(context.Background(), request); err == nil {
		t.Fatal("standalone launch accepted a projection for another workspace")
	}
	request.WorkspacePath = checkout
	if _, err := control.executor(t).CreateInstance(context.Background(), request); err != nil {
		t.Fatalf("create standalone instance: %v", err)
	}
	encoded, err := json.Marshal(control.lastCreateRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	var handoff struct {
		WorkspaceIsolation struct {
			SharedCommonDirs       []string `json:"shared_common_dirs"`
			WritablePaths          []string `json:"writable_paths"`
			PrivateAdminPaths      []string `json:"private_admin_paths"`
			WritableWorkspaceRoots []string `json:"writable_workspace_roots"`
		} `json:"workspace_isolation"`
	}
	if err := json.Unmarshal(encoded, &handoff); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(handoff.WorkspaceIsolation.SharedCommonDirs, projection.SharedCommonDir) ||
		!slices.Contains(handoff.WorkspaceIsolation.WritablePaths, projection.GitDir) ||
		!slices.Contains(handoff.WorkspaceIsolation.PrivateAdminPaths, projection.GitDir) ||
		!slices.Contains(handoff.WorkspaceIsolation.WritableWorkspaceRoots, checkout) {
		t.Fatalf("create handoff lacks the validated Git isolation grant: %+v", handoff.WorkspaceIsolation)
	}
	if err := os.RemoveAll(filepath.Join(checkout, "nested-clone")); err != nil {
		t.Fatal(err)
	}
	privateGitDir := filepath.Join(projection.GitDir, "kandev-agent-git")
	commondir, err := os.ReadFile(filepath.Join(projection.GitDir, "commondir"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(commondir)) != "kandev-agent-git" {
		t.Fatalf("standalone worktree common directory = %q, want task-private metadata", commondir)
	}
	if worktreePath := strings.TrimSpace(string(runContainerGitOutput(t, "--git-dir", privateGitDir, "config", "core.worktree"))); worktreePath != checkout {
		t.Fatalf("standalone Git worktree path = %q, want %q", worktreePath, checkout)
	}
	if err := os.WriteFile(filepath.Join(checkout, "change"), []byte("task change"), 0o600); err != nil {
		t.Fatal(err)
	}
	runContainerGit(t, checkout, "add", "change")
	runContainerGit(t, checkout, "commit", "-m", "task commit")
	if status := strings.TrimSpace(string(runContainerGitOutput(t, "-C", checkout, "status", "--porcelain"))); status != "" {
		t.Fatalf("host checkout status after commit = %q, want clean", status)
	}
	if _, err := worktree.ResolveGitMetadataForRepository(checkout, repo); err != nil {
		t.Fatalf("resolve task worktree after standalone commit: %v", err)
	}
	runContainerGit(t, checkout, "update-ref", "refs/heads/main", "HEAD")
	if siblingAfter, err := os.ReadFile(sharedSiblingRef); err != nil || string(siblingAfter) != string(siblingBefore) {
		t.Fatalf("shared sibling ref changed through task Git metadata: before %q, after %q, err %v", siblingBefore, siblingAfter, err)
	}
	secondProjection, err := worktree.ResolveGitMetadataForRepository(checkout, repo)
	if err != nil {
		t.Fatal(err)
	}
	request.GitMetadataProjections = []*worktree.GitMetadataProjection{secondProjection}
	if _, err := control.executor(t).CreateInstance(context.Background(), request); err != nil {
		t.Fatalf("create second standalone instance: %v", err)
	}
	if status := strings.TrimSpace(string(runContainerGitOutput(t, "-C", checkout, "status", "--porcelain"))); status != "" {
		t.Fatalf("host checkout status after second launch = %q, want clean", status)
	}
	if err := os.WriteFile(filepath.Join(secondProjection.GitDir, "commondir"), []byte("../..\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := control.executor(t).CreateInstance(context.Background(), request); err == nil {
		t.Fatal("standalone launch must reject a swapped Git common-directory pointer")
	}
}

func TestStandaloneExecutorRejectsSymlinkedCheckoutWithoutBinding(t *testing.T) {
	root := t.TempDir()
	checkout := filepath.Join(t.TempDir(), "checkout")
	if err := os.MkdirAll(filepath.Join(checkout, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(checkout, filepath.Join(root, "attached")); err != nil {
		t.Fatal(err)
	}
	control := newStandaloneControlServer(t, true)
	_, err := control.executor(t).CreateInstance(context.Background(), &ExecutorCreateRequest{WorkspacePath: root})
	if err == nil {
		t.Fatal("standalone launch accepted a symlinked Git checkout without a binding")
	}
	if len(control.createRequests) != 0 {
		t.Fatal("standalone launch sent a create request for an unbound checkout")
	}
}

func TestPreparedGitCheckoutPathsPreserveBindingWithoutProjection(t *testing.T) {
	result := &EnvPrepareResult{WorktreeID: "task", WorkspacePath: "/task/checkout"}
	paths := preparedGitCheckoutPaths(result)
	if !slices.Equal(paths, []string{"/task/checkout"}) {
		t.Fatalf("single checkout paths = %v", paths)
	}
	result.Worktrees = []RepoWorktreeResult{
		{WorktreePath: "/task/repo-a"}, {WorktreePath: "/task/repo-b"},
	}
	paths = preparedGitCheckoutPaths(result)
	if !slices.Equal(paths, []string{"/task/repo-a", "/task/repo-b"}) {
		t.Fatalf("multi checkout paths = %v", paths)
	}
}

func TestStandaloneExecutorCarriesIndependentMultiRepoIsolation(t *testing.T) {
	workspace := t.TempDir()
	var projections []*worktree.GitMetadataProjection
	var checkouts []string
	for _, name := range []string{"repo-a", "repo-b"} {
		repo := filepath.Join(t.TempDir(), name)
		runContainerGit(t, "", "init", "-b", "main", repo)
		runContainerGit(t, repo, "config", "user.email", "test@example.com")
		runContainerGit(t, repo, "config", "user.name", "Test")
		if err := os.WriteFile(filepath.Join(repo, "file"), []byte("initial"), 0o600); err != nil {
			t.Fatal(err)
		}
		runContainerGit(t, repo, "add", "file")
		runContainerGit(t, repo, "commit", "-m", "initial")
		checkout := filepath.Join(workspace, name)
		runContainerGit(t, repo, "worktree", "add", "-b", "task", checkout)
		projection, err := worktree.ResolveGitMetadataForRepository(checkout, repo)
		if err != nil {
			t.Fatal(err)
		}
		projections = append(projections, projection)
		checkouts = append(checkouts, checkout)
	}
	control := newStandaloneControlServer(t, true)
	request := &ExecutorCreateRequest{
		InstanceID: "multi-repo", WorkspacePath: workspace,
		PreparedGitCheckouts: checkouts, GitMetadataProjections: projections,
	}
	if _, err := control.executor(t).CreateInstance(context.Background(), request); err != nil {
		t.Fatalf("create multi-repo instance: %v", err)
	}
	isolation := control.lastCreateRequest(t).WorkspaceIsolation
	if isolation == nil || !slices.Equal(isolation.WritableWorkspaceRoots, []string{workspace}) ||
		len(isolation.SharedCommonDirs) != 2 || len(isolation.PrivateAdminPaths) != 2 {
		t.Fatalf("multi-repo isolation = %+v", isolation)
	}
	for _, projection := range projections {
		if !slices.Contains(isolation.SharedCommonDirs, projection.SharedCommonDir) ||
			!slices.Contains(isolation.PrivateAdminPaths, projection.GitDir) {
			t.Fatalf("missing repository isolation for %q: %+v", projection.CheckoutPath, isolation)
		}
	}
	request.GitMetadataProjections = projections[:1]
	if _, err := control.executor(t).CreateInstance(context.Background(), request); err == nil {
		t.Fatal("multi-repo launch accepted a missing attached projection")
	}
	if len(control.createRequests) != 1 {
		t.Fatal("multi-repo launch sent a request after binding rejection")
	}
}
