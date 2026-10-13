package workspaces

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type markerReadHandle struct {
	DirectoryHandle
	afterRead    func()
	beforeVerify func()
	reads        int
}

func (h *markerReadHandle) VerifyFileEntry(name string, file io.ReadCloser) error {
	if h.beforeVerify != nil {
		h.beforeVerify()
	}
	return h.DirectoryHandle.VerifyFileEntry(name, file)
}

func (h *markerReadHandle) OpenFile(name string) (io.ReadCloser, error) {
	opened, err := h.DirectoryHandle.OpenFile(name)
	if err != nil {
		return nil, err
	}
	return &observedMarkerFile{File: opened.(*os.File), owner: h}, nil
}

type observedMarkerFile struct {
	*os.File
	owner *markerReadHandle
}

func (f *observedMarkerFile) Read(buffer []byte) (int, error) {
	n, err := f.File.Read(buffer)
	f.owner.reads++
	if n > 0 && f.owner.afterRead != nil {
		after := f.owner.afterRead
		f.owner.afterRead = nil
		after()
	}
	return n, err
}

func markerReadFixture(t *testing.T, contents string) (DirectoryHandle, string) {
	t.Helper()
	parent := t.TempDir()
	root := filepath.Join(parent, "task")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, OwnershipMarkerFilename)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	handle, err := OpenDirectoryNoFollow(parent, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	return handle, path
}

const validReadMarker = `{"task_id":"task-one","task_dir_name":"task-one","layout_version":1}`

// @covers AC-FOREIGN-GIT-03: cancellation is observed between marker chunks.
func TestOwnershipMarkerCancellationAfterReadStarts(t *testing.T) {
	contents := validReadMarker + strings.Repeat(" ", 16<<10)
	handle, path := markerReadFixture(t, contents)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := &markerReadHandle{DirectoryHandle: handle, afterRead: cancel}
	_, found, err := ReadOwnershipMarkerNoFollowContext(ctx, observed, 64<<10)
	if !errors.Is(err, context.Canceled) || found {
		t.Fatalf("canceled read: found=%v err=%v", found, err)
	}
	if observed.reads != 1 {
		t.Fatalf("continued reading after cancellation: %d reads", observed.reads)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != contents {
		t.Fatalf("marker changed: err=%v", err)
	}
}

// @covers AC-FOREIGN-GIT-03: an in-place ownership rewrite invalidates admission.
func TestOwnershipMarkerRejectsSameInodeRewrite(t *testing.T) {
	handle, path := markerReadFixture(t, validReadMarker)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	replacement := strings.ReplaceAll(validReadMarker, "task-one", "task-two")
	observed := &markerReadHandle{DirectoryHandle: handle, afterRead: func() {
		if err := os.WriteFile(path, []byte(replacement), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
			t.Fatal(err)
		}
	}}
	_, found, err := ReadOwnershipMarkerNoFollowContext(context.Background(), observed, 64<<10)
	if err == nil || found {
		t.Fatalf("accepted rewritten ownership: found=%v err=%v", found, err)
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("rewrite did not preserve inode/size/mtime: %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != replacement {
		t.Fatalf("reader changed replacement marker: %v", err)
	}
}

func TestOwnershipMarkerStableRead(t *testing.T) {
	handle, path := markerReadFixture(t, validReadMarker)
	marker, found, err := ReadOwnershipMarkerNoFollowContext(context.Background(), handle, int64(len(validReadMarker)))
	if err != nil || !found || marker.TaskID != "task-one" {
		t.Fatalf("stable marker rejected: marker=%+v found=%v err=%v", marker, found, err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != validReadMarker {
		t.Fatalf("stable marker changed: %v", err)
	}
}

// Contract coverage for the final state check after entry revalidation.
func TestOwnershipMarkerRejectsRewriteAtFinalVerification(t *testing.T) {
	handle, path := markerReadFixture(t, validReadMarker)
	replacement := strings.ReplaceAll(validReadMarker, "task-one", "task-two")
	observed := &markerReadHandle{DirectoryHandle: handle, beforeVerify: func() {
		if err := os.WriteFile(path, []byte(replacement), 0o600); err != nil {
			t.Fatal(err)
		}
	}}
	_, found, err := ReadOwnershipMarkerNoFollowContext(context.Background(), observed, 64<<10)
	if err == nil || found {
		t.Fatalf("accepted ownership changed at final verification: found=%v err=%v", found, err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != replacement {
		t.Fatalf("reader changed replacement marker: %v", err)
	}
}
