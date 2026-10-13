//go:build !windows

package workspaces

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// @covers AC-FOREIGN-GIT-03: special marker entries cannot block admission or revalidation.
func TestOwnershipMarkerRejectsFIFO(t *testing.T) {
	for _, phase := range []string{"open", "reopen"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOwnershipMarkerFIFOChild$", "--", t.TempDir(), phase)
			output, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("FIFO %s blocked in owned subprocess: %v", phase, ctx.Err())
			}
			if err != nil {
				t.Fatalf("FIFO child failed: %v\n%s", err, output)
			}
		})
	}
}

func TestOwnershipMarkerFIFOChild(t *testing.T) {
	if len(os.Args) < 4 || os.Args[len(os.Args)-3] != "--" {
		return
	}
	parent, phase := os.Args[len(os.Args)-2], os.Args[len(os.Args)-1]
	root := filepath.Join(parent, "task")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	handle, err := OpenDirectoryNoFollow(parent, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	path := filepath.Join(root, OwnershipMarkerFilename)
	var check func() error
	if phase == "reopen" {
		if err := os.WriteFile(path, []byte(validReadMarker), 0o600); err != nil {
			t.Fatal(err)
		}
		file, err := handle.OpenFile(OwnershipMarkerFilename)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = file.Close() })
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		check = func() error { return handle.VerifyFileEntry(OwnershipMarkerFilename, file) }
	} else {
		check = func() error {
			_, _, err := ReadOwnershipMarkerNoFollowContext(context.Background(), handle, 64<<10)
			return err
		}
	}
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := check(); err == nil {
		t.Fatal("accepted FIFO marker")
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) || after.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("FIFO changed during rejection: %v", err)
	}
}
