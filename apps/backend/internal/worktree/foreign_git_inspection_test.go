package worktree

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	storageworkspaces "github.com/kandev/kandev/internal/system/storage/workspaces"
)

// @covers AC-FOREIGN-GIT-01, AC-FOREIGN-GIT-02, AC-FOREIGN-GIT-03, AC-FOREIGN-GIT-05
func TestInspectForeignGitWorktreeCapturesForeignPointerWithoutFollowingAdmin(t *testing.T) {
	repository := initGitRepoForWorktreeTest(t)
	checkout := filepath.Join(t.TempDir(), "original-task")
	foreignCheckout := filepath.Join(t.TempDir(), "foreign-task")
	runGit(t, repository, "worktree", "add", "-b", "foreign-git-original", checkout)
	runGit(t, repository, "worktree", "add", "-b", "foreign-git-owner", foreignCheckout)
	if err := os.WriteFile(filepath.Join(checkout, "tracked.txt"), []byte("preserve me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(checkout, "ignored"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "ignored", "state"), []byte("also preserve me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("tracked.txt", filepath.Join(checkout, "link")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}

	if err := os.WriteFile(filepath.Join(foreignCheckout, "staged.txt"), []byte("foreign index state"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, foreignCheckout, "add", "staged.txt")
	foreignPointer, err := os.ReadFile(filepath.Join(foreignCheckout, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	foreignAdmin := strings.TrimSpace(strings.TrimPrefix(string(foreignPointer), "gitdir:"))
	originalPointer, err := os.ReadFile(filepath.Join(checkout, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	originalAdmin := strings.TrimSpace(strings.TrimPrefix(string(originalPointer), "gitdir:"))
	if err := os.WriteFile(filepath.Join(checkout, ".git"), foreignPointer, 0o600); err != nil {
		t.Fatal(err)
	}

	store := newMockStore()
	updatedAt := time.Date(2026, time.September, 26, 23, 0, 0, 0, time.UTC)
	store.worktrees["original-worktree"] = &Worktree{
		ID: "original-worktree", TaskID: "original-task", TaskEnvironmentID: "original-environment",
		TaskDirName: filepath.Base(filepath.Dir(checkout)), RepositoryID: "repository", RepositoryPath: repository, Path: checkout, UpdatedAt: updatedAt,
	}
	mgr, err := NewManager(prepareInspectionRoot(t, checkout, "original-task"), store, newTestLogger())
	if err != nil {
		t.Fatal(err)
	}

	before := fixtureDigest(t, repository, checkout, originalAdmin, foreignCheckout, foreignAdmin)
	receipt, err := mgr.InspectForeignGitWorktree(context.Background(), ForeignGitInspectionRequest{
		RequestID: "inspection-1", TaskID: "original-task", WorktreeID: "original-worktree",
		ExpectedWorktreeGeneration: updatedAt.Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("InspectForeignGitWorktree: %v", err)
	}
	if !receipt.Complete || receipt.Overall != ForeignGitInspectionBlocked {
		t.Fatalf("receipt completion = %v, overall = %q; want complete physical evidence blocked on foreign admin", receipt.Complete, receipt.Overall)
	}
	if receipt.Pointer.AdminPath != foreignAdmin || receipt.Pointer.Backlink != "" {
		t.Fatalf("pointer evidence = %+v", receipt.Pointer)
	}
	if receipt.Pointer.ForeignTaskID != "" || receipt.Pointer.ForeignTaskEvidence != ForeignGitInspectionUnknown || receipt.Pointer.Status != ForeignGitInspectionUnknown {
		t.Fatalf("foreign ownership = %+v; want unknown", receipt.Pointer)
	}
	if receipt.Pointer.ReasonCode != "FOREIGN_ADMIN_UNAUTHORIZED" {
		t.Fatalf("pointer reason code = %q, want FOREIGN_ADMIN_UNAUTHORIZED", receipt.Pointer.ReasonCode)
	}
	if len(receipt.Entries) != 5 { // README, tracked file, ignored directory/file, symlink; top-level .git is metadata.
		t.Fatalf("entry count = %d, want 5: %+v", len(receipt.Entries), receipt.Entries)
	}
	if got := fixtureDigest(t, repository, checkout, originalAdmin, foreignCheckout, foreignAdmin); got != before {
		t.Fatalf("inspection mutated fixture: before %s, after %s", before, got)
	}
}

// @covers AC-FOREIGN-GIT-01, AC-FOREIGN-GIT-08
func TestInspectForeignGitWorktreeRejectsStaleGenerationAndNonreciprocalPointer(t *testing.T) {
	checkout := t.TempDir()
	foreignAdmin := filepath.Join(t.TempDir(), "admin")
	if err := os.MkdirAll(foreignAdmin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(foreignAdmin, "gitdir"), []byte(filepath.Join(t.TempDir(), "other", ".git")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, ".git"), []byte("gitdir: "+foreignAdmin+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	updatedAt := time.Date(2026, time.September, 26, 23, 0, 0, 0, time.UTC)
	store := newMockStore()
	store.worktrees["original-worktree"] = &Worktree{ID: "original-worktree", TaskID: "original-task", TaskDirName: filepath.Base(filepath.Dir(checkout)), RepositoryID: "repository", Path: checkout, UpdatedAt: updatedAt}
	mgr, err := NewManager(prepareInspectionRoot(t, checkout, "original-task"), store, newTestLogger())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := mgr.InspectForeignGitWorktree(context.Background(), ForeignGitInspectionRequest{
		RequestID: "inspection-1", TaskID: "original-task", WorktreeID: "original-worktree", ExpectedWorktreeGeneration: "stale",
	}); err == nil {
		t.Fatal("accepted stale generation")
	}

	receipt, err := mgr.InspectForeignGitWorktree(context.Background(), ForeignGitInspectionRequest{
		RequestID: "inspection-2", TaskID: "original-task", WorktreeID: "original-worktree", ExpectedWorktreeGeneration: updatedAt.Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("InspectForeignGitWorktree: %v", err)
	}
	if !receipt.Complete || receipt.Overall != ForeignGitInspectionBlocked || receipt.Pointer.Reciprocal || receipt.Pointer.Status != ForeignGitInspectionUnknown {
		t.Fatalf("nonreciprocal receipt = %+v", receipt.Pointer)
	}
	if receipt.Pointer.ReasonCode != "FOREIGN_ADMIN_UNAUTHORIZED" {
		t.Fatalf("pointer reason code = %q, want FOREIGN_ADMIN_UNAUTHORIZED", receipt.Pointer.ReasonCode)
	}
}

// @covers AC-FOREIGN-GIT-03
func TestInspectForeignGitWorktreeMarksNestedGitBoundaryIncomplete(t *testing.T) {
	checkout := t.TempDir()
	if err := os.MkdirAll(filepath.Join(checkout, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "nested", ".git"), []byte("gitdir: elsewhere\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	admin := filepath.Join(t.TempDir(), "admin")
	if err := os.MkdirAll(admin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, ".git"), []byte("gitdir: "+admin+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	updatedAt := time.Date(2026, time.September, 26, 23, 0, 0, 0, time.UTC)
	store := newMockStore()
	store.worktrees["original-worktree"] = &Worktree{ID: "original-worktree", TaskID: "original-task", TaskDirName: filepath.Base(filepath.Dir(checkout)), RepositoryID: "repository", Path: checkout, UpdatedAt: updatedAt}
	mgr, err := NewManager(prepareInspectionRoot(t, checkout, "original-task"), store, newTestLogger())
	if err != nil {
		t.Fatal(err)
	}

	receipt, err := mgr.InspectForeignGitWorktree(context.Background(), ForeignGitInspectionRequest{
		RequestID: "inspection-3", TaskID: "original-task", WorktreeID: "original-worktree", ExpectedWorktreeGeneration: updatedAt.Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("InspectForeignGitWorktree: %v", err)
	}
	if receipt.Complete || receipt.Overall != ForeignGitInspectionUnknown || !hasReason(receipt.ReasonCodes, "NESTED_GIT_BOUNDARY") {
		t.Fatalf("nested Git receipt = %+v", receipt)
	}
}

// @covers AC-WORKSPACES-FOREIGN-GIT-RECOVERY-002.2
func TestInspectForeignGitWorktreeRejectsSymlinkedCheckoutRoot(t *testing.T) {
	target := t.TempDir()
	checkout := filepath.Join(t.TempDir(), "checkout-link")
	if err := os.Symlink(target, checkout); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	manager, generation := inspectionManager(t, checkout)
	receipt, err := manager.InspectForeignGitWorktree(context.Background(), ForeignGitInspectionRequest{
		RequestID: "inspection-symlink-root", TaskID: "original-task", WorktreeID: "original-worktree",
		ExpectedWorktreeGeneration: generation,
	})
	if err != nil {
		t.Fatalf("InspectForeignGitWorktree: %v", err)
	}
	if receipt.Complete || receipt.Overall != ForeignGitInspectionUnknown || !hasReason(receipt.ReasonCodes, "CHECKOUT_OPEN_FAILED") {
		t.Fatalf("symlinked checkout receipt = %+v", receipt)
	}
}

// @covers AC-WORKSPACES-FOREIGN-GIT-RECOVERY-002.2
func TestInspectForeignGitWorktreeMarksCanceledWalkIncomplete(t *testing.T) {
	checkout := t.TempDir()
	if err := os.WriteFile(filepath.Join(checkout, "source.txt"), []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, generation := inspectionManager(t, checkout)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	receipt, err := manager.InspectForeignGitWorktree(ctx, ForeignGitInspectionRequest{
		RequestID: "inspection-canceled", TaskID: "original-task", WorktreeID: "original-worktree",
		ExpectedWorktreeGeneration: generation,
	})
	if err != nil {
		t.Fatalf("InspectForeignGitWorktree: %v", err)
	}
	if receipt.Complete || receipt.Overall != ForeignGitInspectionUnknown || !hasReason(receipt.ReasonCodes, "SOURCE_WALK_INCOMPLETE") {
		t.Fatalf("canceled walk receipt = %+v", receipt)
	}
}

// @covers AC-FOREIGN-GIT-03
func TestInspectForeignGitWorktreeRequiresPinnedTaskRootOwnership(t *testing.T) {
	tasksBase := t.TempDir()
	checkout := filepath.Join(tasksBase, "original-task-dir", "repo")
	if err := os.MkdirAll(checkout, 0o700); err != nil {
		t.Fatal(err)
	}
	updatedAt := time.Date(2026, time.September, 26, 23, 0, 0, 0, time.UTC)
	store := newMockStore()
	store.worktrees["original-worktree"] = &Worktree{ID: "original-worktree", TaskID: "original-task", TaskDirName: "original-task-dir", RepositoryID: "repository", Path: checkout, UpdatedAt: updatedAt}
	manager, err := NewManager(Config{Enabled: true, TasksBasePath: tasksBase, BranchPrefix: "kandev/"}, store, newTestLogger())
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := manager.InspectForeignGitWorktree(context.Background(), ForeignGitInspectionRequest{RequestID: "inspection-marker", TaskID: "original-task", WorktreeID: "original-worktree", ExpectedWorktreeGeneration: updatedAt.Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatalf("InspectForeignGitWorktree: %v", err)
	}
	if receipt.Complete || receipt.Overall != ForeignGitInspectionUnknown || !hasReason(receipt.ReasonCodes, "TASK_ROOT_OWNERSHIP_UNKNOWN") {
		t.Fatalf("missing ownership receipt = %+v", receipt)
	}
}

// @covers AC-FOREIGN-GIT-03
func TestInspectForeignGitWorktreeBoundsPointerFile(t *testing.T) {
	checkout := t.TempDir()
	if err := os.WriteFile(filepath.Join(checkout, ".git"), make([]byte, foreignGitInspectionPointerByteLimit+1), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, generation := inspectionManager(t, checkout)
	receipt, err := manager.InspectForeignGitWorktree(context.Background(), ForeignGitInspectionRequest{RequestID: "inspection-pointer-bound", TaskID: "original-task", WorktreeID: "original-worktree", ExpectedWorktreeGeneration: generation})
	if err != nil {
		t.Fatalf("InspectForeignGitWorktree: %v", err)
	}
	if receipt.Complete || !hasReason(receipt.ReasonCodes, "GIT_POINTER_UNAVAILABLE") {
		t.Fatalf("oversized pointer receipt = %+v", receipt)
	}
}

func inspectionManager(t *testing.T, checkout string) (*Manager, string) {
	t.Helper()
	updatedAt := time.Date(2026, time.September, 26, 23, 0, 0, 0, time.UTC)
	store := newMockStore()
	store.worktrees["original-worktree"] = &Worktree{
		ID: "original-worktree", TaskID: "original-task", TaskDirName: filepath.Base(filepath.Dir(checkout)), RepositoryID: "repository", Path: checkout, UpdatedAt: updatedAt,
	}
	manager, err := NewManager(prepareInspectionRoot(t, checkout, "original-task"), store, newTestLogger())
	if err != nil {
		t.Fatal(err)
	}
	return manager, updatedAt.Format(time.RFC3339Nano)
}

func prepareInspectionRoot(t *testing.T, checkout, taskID string) Config {
	t.Helper()
	taskRoot := filepath.Dir(checkout)
	if err := storageworkspaces.WriteOwnershipMarker(taskRoot, storageworkspaces.OwnershipMarker{
		TaskID: taskID, TaskDirName: filepath.Base(taskRoot), LayoutVersion: storageworkspaces.LayoutVersionSemantic,
	}); err != nil {
		t.Fatalf("write inspection ownership marker: %v", err)
	}
	return Config{Enabled: true, TasksBasePath: filepath.Dir(taskRoot), BranchPrefix: "kandev/"}
}

// @covers AC-WORKSPACES-FOREIGN-GIT-RECOVERY-002.2
func TestForeignGitInspectionDigestFileBoundsReadsByReportedSize(t *testing.T) {
	file := &inspectionTestFile{size: 2 << 30}
	if _, _, err := foreignGitInspectionDigestFile(file); err == nil {
		t.Fatal("digest accepted a file larger than the inspection byte limit")
	}
	if file.readCalls != 0 {
		t.Fatalf("read calls = %d, want no content reads for an oversized file", file.readCalls)
	}
}

// @covers AC-WORKSPACES-FOREIGN-GIT-RECOVERY-002.2
func TestForeignGitInspectionDigestFileRejectsChangedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "changing")
	if err := os.WriteFile(path, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := &inspectionChangingFile{File: file, path: path}
	if _, _, err := foreignGitInspectionDigestFile(changed); err == nil {
		t.Fatal("digest accepted a file that changed during capture")
	}
}

type inspectionTestFile struct {
	size      int64
	readCalls int
}

func (f *inspectionTestFile) Read([]byte) (int, error) {
	f.readCalls++
	return 0, io.EOF
}

func (*inspectionTestFile) Close() error { return nil }

func (f *inspectionTestFile) Stat() (os.FileInfo, error) {
	return inspectionTestFileInfo{size: f.size}, nil
}

type inspectionChangingFile struct {
	*os.File
	path string
}

func (f *inspectionChangingFile) Read(buffer []byte) (int, error) {
	n, err := f.File.Read(buffer)
	if n > 0 {
		if writeErr := os.WriteFile(f.path, []byte("changed while hashing"), 0o600); writeErr != nil {
			return n, writeErr
		}
	}
	return n, err
}

type inspectionTestFileInfo struct {
	size int64
}

func (f inspectionTestFileInfo) Name() string     { return "inspection-test-file" }
func (f inspectionTestFileInfo) Size() int64      { return f.size }
func (inspectionTestFileInfo) Mode() os.FileMode  { return 0o600 }
func (inspectionTestFileInfo) ModTime() time.Time { return time.Time{} }
func (inspectionTestFileInfo) IsDir() bool        { return false }
func (inspectionTestFileInfo) Sys() any           { return nil }

func hasReason(reasons []string, want string) bool {
	for _, reason := range reasons {
		if reason == want {
			return true
		}
	}
	return false
}

func fixtureDigest(t *testing.T, paths ...string) string {
	t.Helper()
	h := sha256.New()
	for _, path := range paths {
		if err := filepath.Walk(path, func(name string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(h, "%s|%s|%o|", name, info.Mode().Type(), info.Mode())
			if info.Mode().IsRegular() {
				data, readErr := os.ReadFile(name)
				if readErr != nil {
					return readErr
				}
				_, _ = h.Write(data)
			}
			if info.Mode()&os.ModeSymlink != 0 {
				target, linkErr := os.Readlink(name)
				if linkErr != nil {
					return linkErr
				}
				_, _ = h.Write([]byte(target))
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}
