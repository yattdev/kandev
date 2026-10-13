package worktree

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	storageworkspaces "github.com/kandev/kandev/internal/system/storage/workspaces"
)

type inspectionCancelFile struct {
	*os.File
	afterRead context.CancelFunc
	reads     int
	finalEOF  bool
}

func (file *inspectionCancelFile) Read(buffer []byte) (int, error) {
	n, err := file.File.Read(buffer)
	file.reads++
	if n > 0 && file.afterRead != nil {
		file.afterRead()
		file.afterRead = nil
		if file.finalEOF {
			err = io.EOF
		}
	}
	return n, err
}

// @covers AC-FOREIGN-GIT-03: cancellation accompanying final bytes invalidates a hash.
func TestForeignGitInspectionDigestRejectsCancellationAtEOF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(path, []byte("original source"), 0o600); err != nil {
		t.Fatal(err)
	}
	opened, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	file := &inspectionCancelFile{File: opened, afterRead: cancel, finalEOF: true}
	_, _, err = foreignGitInspectionDigestFileLimit(ctx, file, 1024)
	if !errors.Is(err, context.Canceled) || file.reads != 1 {
		t.Fatalf("final-read cancellation accepted: reads=%d err=%v", file.reads, err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "original source" {
		t.Fatalf("canceled read changed source: %v", err)
	}
}

type inspectionCancelDirectory struct {
	storageworkspaces.IdentityDirectoryHandle
	ctxCancel    context.CancelFunc
	phase        string
	pointerOpens int
	files        []*inspectionCancelFile
}

func (directory *inspectionCancelDirectory) OpenFile(name string) (io.ReadCloser, error) {
	opened, err := directory.IdentityDirectoryHandle.OpenFile(name)
	if err != nil {
		return nil, err
	}
	file := &inspectionCancelFile{File: opened.(*os.File)}
	if name == recoveryGitDirName {
		directory.pointerOpens++
	}
	switch {
	case directory.phase == "read" && name == "a-source":
		file.afterRead = directory.ctxCancel
	case directory.phase == "walk" && name == "b-source":
		directory.ctxCancel()
	case directory.phase == "final" && name == recoveryGitDirName && directory.pointerOpens == 2:
		file.afterRead = directory.ctxCancel
	}
	directory.files = append(directory.files, file)
	return file, nil
}

// Contract coverage for cancellation at real read/walk/final-observation boundaries.
func TestForeignGitInspectionCancelsInProgress(t *testing.T) {
	for _, phase := range []string{"read", "walk", "final"} {
		t.Run(phase, func(t *testing.T) {
			parent := t.TempDir()
			checkout := filepath.Join(parent, "checkout")
			if err := os.Mkdir(checkout, 0o700); err != nil {
				t.Fatal(err)
			}
			for name, contents := range map[string]string{
				".git": "gitdir: /unopened/admin\n", "a-source": "original a", "b-source": "original b",
			} {
				if err := os.WriteFile(filepath.Join(checkout, name), []byte(contents), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before := fixtureDigest(t, checkout)
			opened, err := storageworkspaces.OpenDirectoryNoFollow(parent, checkout)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = opened.Close() })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			directory := &inspectionCancelDirectory{IdentityDirectoryHandle: opened.(storageworkspaces.IdentityDirectoryHandle), ctxCancel: cancel, phase: phase}
			receipt := &ForeignGitInspectionReceipt{Overall: ForeignGitInspectionUnknown}
			foreignGitInspectionCapture(ctx, directory, checkout, receipt)
			if receipt.Complete || receipt.Overall != ForeignGitInspectionUnknown || ctx.Err() == nil {
				t.Fatalf("in-progress %s cancellation accepted: %+v", phase, receipt)
			}
			if phase == "final" && (!hasReason(receipt.ReasonCodes, "SOURCE_REVALIDATION_FAILED") || directory.pointerOpens != 2) {
				t.Fatalf("final pass did not execute/cancel: %+v", receipt)
			}
			if phase == "walk" && len(receipt.Entries) != 1 {
				t.Fatalf("walk did not cancel between actual entries: %+v", receipt.Entries)
			}
			for _, file := range directory.files {
				if file.afterRead != nil {
					t.Fatal("configured read cancellation was not reached")
				}
			}
			if after := fixtureDigest(t, checkout); after != before {
				t.Fatal("canceled inspection changed source fixture")
			}
		})
	}
}
