//go:build !windows

package worktree

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	storageworkspaces "github.com/kandev/kandev/internal/system/storage/workspaces"
)

type inspectionBarrierDirectory struct {
	storageworkspaces.IdentityDirectoryHandle
	beforeRead func()
}

func (h *inspectionBarrierDirectory) OpenFile(name string) (io.ReadCloser, error) {
	if name == "z-barrier" && h.beforeRead != nil {
		barrier := h.beforeRead
		h.beforeRead = nil
		barrier()
	}
	return h.IdentityDirectoryHandle.OpenFile(name)
}

// @covers AC-FOREIGN-GIT-03: same-content replacement is not stable object evidence.
func TestForeignGitInspectionRejectsLateReplacement(t *testing.T) {
	for _, scenario := range []string{"file", "link", "directory", "pointer", "mode", "stable"} {
		t.Run(scenario, func(t *testing.T) {
			parent := t.TempDir()
			checkout := filepath.Join(parent, "checkout")
			if err := os.MkdirAll(filepath.Join(checkout, "a-directory"), 0o700); err != nil {
				t.Fatal(err)
			}
			for name, contents := range map[string]string{
				".git": "gitdir: /unopened/foreign-admin\n", "a-file": "same bytes", "a-directory/child": "same tree", "z-barrier": "barrier",
			} {
				if err := os.WriteFile(filepath.Join(checkout, name), []byte(contents), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink("a-file", filepath.Join(checkout, "a-link")); err != nil {
				t.Fatal(err)
			}
			foreign := filepath.Join(parent, "foreign")
			if err := os.Mkdir(foreign, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(foreign, "index-ref-queue-sentinel"), []byte("preserve foreign evidence"), 0o600); err != nil {
				t.Fatal(err)
			}
			foreignBefore := fixtureDigest(t, foreign)
			handle, err := storageworkspaces.OpenDirectoryNoFollow(parent, checkout)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = handle.Close() })
			var fixtureBefore string
			wrapped := &inspectionBarrierDirectory{IdentityDirectoryHandle: handle.(storageworkspaces.IdentityDirectoryHandle), beforeRead: func() {
				mutateInspectionFixture(t, parent, checkout, scenario)
				fixtureBefore = fixtureDigest(t, parent)
			}}
			receipt := &ForeignGitInspectionReceipt{Overall: ForeignGitInspectionUnknown}
			foreignGitInspectionCapture(context.Background(), wrapped, checkout, receipt)
			if scenario == "stable" {
				if !receipt.Complete || receipt.Overall != ForeignGitInspectionBlocked {
					t.Fatalf("stable evidence rejected: %+v", receipt)
				}
			} else if receipt.Complete || receipt.Overall != ForeignGitInspectionUnknown {
				t.Fatalf("accepted late %s replacement: %+v", scenario, receipt)
			}
			if after := fixtureDigest(t, foreign); after != foreignBefore {
				t.Fatal("inspection changed foreign fixture")
			}
			if after := fixtureDigest(t, parent); fixtureBefore == "" || after != fixtureBefore {
				t.Fatal("inspection changed original, preserved, or replacement fixture bytes/modes")
			}
		})
	}
}

func mutateInspectionFixture(t *testing.T, parent, checkout, scenario string) {
	t.Helper()
	var name string
	switch scenario {
	case "file":
		name = "a-file"
	case "link":
		name = "a-link"
	case "directory":
		name = "a-directory"
	case "pointer":
		name = ".git"
	case "mode":
		if err := os.Chmod(filepath.Join(checkout, "a-file"), 0o640); err != nil {
			t.Fatal(err)
		}
		return
	case "stable":
		return
	}
	original := filepath.Join(checkout, name)
	preserved := filepath.Join(parent, "preserved-object")
	if err := os.Rename(original, preserved); err != nil {
		t.Fatal(err)
	}
	switch scenario {
	case "link":
		if err := os.Symlink("a-file", original); err != nil {
			t.Fatal(err)
		}
	case "directory":
		if err := os.Mkdir(original, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(original, "child"), []byte("same tree"), 0o600); err != nil {
			t.Fatal(err)
		}
	default:
		contents, err := os.ReadFile(preserved)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(original, contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

type inspectionDescentBarrier struct {
	storageworkspaces.IdentityDirectoryHandle
	beforeDescent func()
}

func (h *inspectionDescentBarrier) OpenSubdirectory(name string) (storageworkspaces.DirectoryHandle, error) {
	if h.beforeDescent != nil {
		barrier := h.beforeDescent
		h.beforeDescent = nil
		barrier()
	}
	return h.IdentityDirectoryHandle.OpenSubdirectory(name)
}

// @covers AC-FOREIGN-GIT-03: checkout descent remains rooted in admitted ownership.
func TestInspectForeignGitRetainsAdmittedRoot(t *testing.T) {
	for _, scenario := range []string{"foreign-root", "same-tree-root", "stable"} {
		t.Run(scenario, func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "task")
			checkout := filepath.Join(root, "repo")
			if err := os.MkdirAll(checkout, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(checkout, ".git"), []byte("gitdir: /unopened/admin\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(checkout, "source"), []byte("original source"), 0o600); err != nil {
				t.Fatal(err)
			}
			manager, generation := inspectionManager(t, checkout)
			replacement := filepath.Join(base, "replacement")
			if err := os.MkdirAll(filepath.Join(replacement, "repo"), 0o700); err != nil {
				t.Fatal(err)
			}
			marker, err := os.ReadFile(filepath.Join(root, storageworkspaces.OwnershipMarkerFilename))
			if err != nil {
				t.Fatal(err)
			}
			contents := "original source"
			if scenario == "foreign-root" {
				marker = []byte(`{"task_id":"foreign-task","task_dir_name":"task","layout_version":1}`)
				contents = "foreign source"
			}
			for name, data := range map[string][]byte{
				storageworkspaces.OwnershipMarkerFilename: marker, "repo/source": []byte(contents), "repo/.git": []byte("gitdir: /unopened/admin\n"),
			} {
				if err := os.WriteFile(filepath.Join(replacement, name), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			preserved := filepath.Join(base, "preserved-root")
			var before string
			open := func(parent, target string) (storageworkspaces.DirectoryHandle, error) {
				handle, err := storageworkspaces.OpenDirectoryNoFollow(parent, target)
				if err != nil || target != root {
					return handle, err
				}
				return &inspectionDescentBarrier{IdentityDirectoryHandle: handle.(storageworkspaces.IdentityDirectoryHandle), beforeDescent: func() {
					if scenario != "stable" {
						if err := os.Rename(root, preserved); err != nil {
							t.Fatal(err)
						}
						if err := os.Rename(replacement, root); err != nil {
							t.Fatal(err)
						}
						before = fixtureDigest(t, root, preserved)
					} else {
						before = fixtureDigest(t, root, replacement)
					}
				}}, nil
			}
			receipt, err := manager.inspectForeignGitWorktree(context.Background(), ForeignGitInspectionRequest{
				RequestID: "retained-root", TaskID: "original-task", WorktreeID: "original-worktree", ExpectedWorktreeGeneration: generation,
			}, open)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "stable" {
				if !receipt.Complete || receipt.Overall != ForeignGitInspectionBlocked || fixtureDigest(t, root, replacement) != before {
					t.Fatalf("stable retained root rejected or mutated: %+v", receipt)
				}
				return
			}
			if receipt.Complete || receipt.Overall != ForeignGitInspectionUnknown || !hasReason(receipt.ReasonCodes, "TASK_ROOT_OWNERSHIP_CHANGED") {
				t.Fatalf("accepted admitted-root replacement: %+v", receipt)
			}
			if fixtureDigest(t, root, preserved) != before {
				t.Fatal("inspection mutated original or replacement root")
			}
			for _, entry := range receipt.Entries {
				if entry.Path == "source" && entry.ContentSHA256 != foreignGitInspectionSHA256([]byte("original source")) {
					t.Fatal("read foreign source after root admission")
				}
			}
		})
	}
}

type inspectionUnavailableIdentity struct {
	storageworkspaces.IdentityDirectoryHandle
}

func (*inspectionUnavailableIdentity) Snapshot() (storageworkspaces.ObjectSnapshot, error) {
	return storageworkspaces.ObjectSnapshot{}, errors.New("native metadata unavailable")
}

func TestInspectForeignGitRejectsUnavailableIdentity(t *testing.T) {
	checkout := t.TempDir()
	manager, generation := inspectionManager(t, checkout)
	open := func(parent, target string) (storageworkspaces.DirectoryHandle, error) {
		handle, err := storageworkspaces.OpenDirectoryNoFollow(parent, target)
		if err != nil {
			return nil, err
		}
		return &inspectionUnavailableIdentity{IdentityDirectoryHandle: handle.(storageworkspaces.IdentityDirectoryHandle)}, nil
	}
	receipt, err := manager.inspectForeignGitWorktree(context.Background(), ForeignGitInspectionRequest{
		RequestID: "identity-unavailable", TaskID: "original-task", WorktreeID: "original-worktree", ExpectedWorktreeGeneration: generation,
	}, open)
	if err != nil || receipt.Complete || receipt.Overall != ForeignGitInspectionUnknown || !hasReason(receipt.ReasonCodes, "TASK_ROOT_OWNERSHIP_UNKNOWN") {
		t.Fatalf("unavailable native identity accepted: receipt=%+v err=%v", receipt, err)
	}
}

// Contract coverage at the final owner/generation boundary after both walks.
func TestInspectForeignGitRechecksOwnerAndGeneration(t *testing.T) {
	for _, change := range []string{"marker", "generation"} {
		t.Run(change, func(t *testing.T) {
			checkout := t.TempDir()
			if err := os.WriteFile(filepath.Join(checkout, ".git"), []byte("gitdir: /unopened/admin\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			manager, generation := inspectionManager(t, checkout)
			markerPath := filepath.Join(filepath.Dir(checkout), storageworkspaces.OwnershipMarkerFilename)
			markerBytes, err := os.ReadFile(markerPath)
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(markerPath)
			if err != nil {
				t.Fatal(err)
			}
			opens := 0
			var preserved string
			open := func(parent, target string) (storageworkspaces.DirectoryHandle, error) {
				opens++
				if opens != 2 {
					return storageworkspaces.OpenDirectoryNoFollow(parent, target)
				}
				if change == "marker" {
					replacement := strings.ReplaceAll(string(markerBytes), "original-task", "otherone-task")
					if err := os.WriteFile(markerPath, []byte(replacement), 0o600); err != nil {
						t.Fatal(err)
					}
					if err := os.Chtimes(markerPath, before.ModTime(), before.ModTime()); err != nil {
						t.Fatal(err)
					}
				} else {
					row := manager.store.(*mockStore).worktrees["original-worktree"]
					row.UpdatedAt = row.UpdatedAt.Add(time.Second)
				}
				preserved = fixtureDigest(t, filepath.Dir(checkout))
				return storageworkspaces.OpenDirectoryNoFollow(parent, target)
			}
			receipt, err := manager.inspectForeignGitWorktree(context.Background(), ForeignGitInspectionRequest{
				RequestID: "final-owner", TaskID: "original-task", WorktreeID: "original-worktree", ExpectedWorktreeGeneration: generation,
			}, open)
			if err != nil || receipt.Complete || receipt.Overall != ForeignGitInspectionUnknown {
				t.Fatalf("accepted changed final owner/generation: receipt=%+v err=%v", receipt, err)
			}
			reason := "WORKTREE_GENERATION_CHANGED"
			if change == "marker" {
				reason = "TASK_ROOT_OWNERSHIP_CHANGED"
				after, err := os.Stat(markerPath)
				if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
					t.Fatalf("marker rewrite did not preserve inode/size/mtime: %v", err)
				}
			}
			if !hasReason(receipt.ReasonCodes, reason) || opens != 2 || fixtureDigest(t, filepath.Dir(checkout)) != preserved {
				t.Fatalf("final revalidation did not reject/preserve %s: %+v", change, receipt)
			}
		})
	}
}
