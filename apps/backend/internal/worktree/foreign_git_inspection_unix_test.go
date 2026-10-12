//go:build !windows

package worktree

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// @covers AC-WORKSPACES-FOREIGN-GIT-RECOVERY-002.2
func TestInspectForeignGitWorktreeRejectsSpecialFile(t *testing.T) {
	checkout := t.TempDir()
	if err := os.WriteFile(filepath.Join(checkout, ".git"), []byte("gitdir: /unopened/admin\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(checkout, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, generation := inspectionManager(t, checkout)
	receipt, err := manager.InspectForeignGitWorktree(context.Background(), ForeignGitInspectionRequest{
		RequestID: "inspection-special-file", TaskID: "original-task", WorktreeID: "original-worktree",
		ExpectedWorktreeGeneration: generation,
	})
	if err != nil {
		t.Fatalf("InspectForeignGitWorktree: %v", err)
	}
	if receipt.Complete || receipt.Overall != ForeignGitInspectionUnknown || !hasReason(receipt.ReasonCodes, "SOURCE_WALK_INCOMPLETE") {
		t.Fatalf("special file receipt = %+v", receipt)
	}
}
