//go:build !windows

package worktree

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectForeignGitEntryLimit(t *testing.T) {
	checkout := t.TempDir()
	if err := os.WriteFile(filepath.Join(checkout, ".git"), []byte("gitdir: /unopened/admin\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for index := 0; index <= foreignGitInspectionEntryLimit; index++ {
		path := filepath.Join(checkout, fmt.Sprintf("entry-%06d", index))
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	manager, generation := inspectionManager(t, checkout)
	before := fixtureDigest(t, checkout)
	receipt, err := manager.InspectForeignGitWorktree(context.Background(), ForeignGitInspectionRequest{
		RequestID: "entry-limit", TaskID: "original-task", WorktreeID: "original-worktree", ExpectedWorktreeGeneration: generation,
	})
	if err != nil || receipt.Complete || receipt.Overall != ForeignGitInspectionUnknown || !hasReason(receipt.ReasonCodes, "SOURCE_ENTRY_LIMIT") {
		t.Fatalf("entry limit accepted: receipt=%+v err=%v", receipt, err)
	}
	if after := fixtureDigest(t, checkout); after != before {
		t.Fatal("entry-limit rejection mutated fixture")
	}
}

// The total-byte boundary uses four real sparse files, not an invented counter.
// They allocate no source payload, but all four GiB are streamed through the walker.
func TestInspectForeignGitByteLimits(t *testing.T) {
	for _, scenario := range []string{"per-file", "total"} {
		t.Run(scenario, func(t *testing.T) {
			checkout := t.TempDir()
			if err := os.WriteFile(filepath.Join(checkout, ".git"), []byte("gitdir: /unopened/admin\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			count, size := 1, foreignGitInspectionFileByteLimit+1
			if scenario == "total" {
				count, size = 4, foreignGitInspectionFileByteLimit
			}
			before := make(map[string]os.FileInfo)
			for index := 0; index < count; index++ {
				path := filepath.Join(checkout, fmt.Sprintf("source-%d", index))
				file, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := file.Truncate(size); err != nil {
					_ = file.Close()
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				before[path] = info
			}
			if err := os.WriteFile(filepath.Join(checkout, "z-overflow"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			manager, generation := inspectionManager(t, checkout)
			receipt, err := manager.InspectForeignGitWorktree(context.Background(), ForeignGitInspectionRequest{
				RequestID: "byte-limit", TaskID: "original-task", WorktreeID: "original-worktree", ExpectedWorktreeGeneration: generation,
			})
			if err != nil || receipt.Complete || receipt.Overall != ForeignGitInspectionUnknown || !hasReason(receipt.ReasonCodes, "SOURCE_BYTE_LIMIT") {
				t.Fatalf("%s byte limit accepted: receipt=%+v err=%v", scenario, receipt, err)
			}
			if scenario == "total" && len(receipt.Entries) != 4 {
				t.Fatalf("total boundary did not read four admissible files: %+v", receipt.Entries)
			}
			for path, original := range before {
				current, err := os.Stat(path)
				if err != nil || !os.SameFile(original, current) || original.Size() != current.Size() || original.Mode() != current.Mode() || !original.ModTime().Equal(current.ModTime()) {
					t.Fatalf("byte-limit rejection changed fixture file: %v", err)
				}
			}
			if got, err := os.ReadFile(filepath.Join(checkout, "z-overflow")); err != nil || string(got) != "x" {
				t.Fatalf("overflow sentinel changed: %v", err)
			}
		})
	}
}
