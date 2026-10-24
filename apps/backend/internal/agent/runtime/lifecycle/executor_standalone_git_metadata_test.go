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
	}
	request.GitMetadataProjections = nil
	if _, err := control.executor(t).CreateInstance(context.Background(), request); err == nil {
		t.Fatal("standalone launch accepted a linked checkout without its trusted projection")
	}
	request.GitMetadataProjections = []*worktree.GitMetadataProjection{projection}
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
			SharedCommonDirs  []string `json:"shared_common_dirs"`
			WritablePaths     []string `json:"writable_paths"`
			PrivateAdminPaths []string `json:"private_admin_paths"`
		} `json:"workspace_isolation"`
	}
	if err := json.Unmarshal(encoded, &handoff); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(handoff.WorkspaceIsolation.SharedCommonDirs, projection.SharedCommonDir) ||
		!slices.Contains(handoff.WorkspaceIsolation.WritablePaths, projection.GitDir) ||
		!slices.Contains(handoff.WorkspaceIsolation.PrivateAdminPaths, projection.GitDir) {
		t.Fatalf("create handoff lacks the validated Git isolation grant: %+v", handoff.WorkspaceIsolation)
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
