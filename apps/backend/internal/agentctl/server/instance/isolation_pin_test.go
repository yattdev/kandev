//go:build linux

package instance

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kandev/kandev/internal/agentctl/server/config"
	"github.com/kandev/kandev/internal/agentctl/server/process/isolation"
	"golang.org/x/sys/unix"
)

func TestCreateInstanceRejectsReplacedPinnedGrantBeforePublishing(t *testing.T) {
	for _, replacement := range []string{"directory", "symlink"} {
		t.Run(replacement, func(t *testing.T) {
			root := t.TempDir()
			shared := filepath.Join(root, "repo", ".git")
			private := filepath.Join(shared, "worktrees", "task")
			workspace := filepath.Join(root, "task")
			for _, path := range []string{private, workspace} {
				if err := os.MkdirAll(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			manifest := &config.WorkspaceIsolation{
				SharedCommonDirs:       []string{shared},
				WritablePaths:          []string{private},
				PrivateAdminPaths:      []string{private},
				WritableWorkspaceRoots: []string{workspace},
			}
			if err := manifest.CaptureObjects(); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(workspace, workspace+"-old"); err != nil {
				t.Fatal(err)
			}
			if replacement == "symlink" {
				if err := os.Symlink(workspace+"-old", workspace); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Mkdir(workspace, 0700); err != nil {
				t.Fatal(err)
			}
			mgr := NewManager(&config.Config{Ports: config.PortConfig{Base: 0, Max: 0}}, newTestLogger(t))
			t.Cleanup(func() { _ = mgr.Shutdown(context.Background()) })
			if _, err := mgr.CreateInstance(context.Background(), &CreateRequest{
				WorkspacePath: workspace, WorkspaceIsolation: manifest,
			}); err == nil {
				t.Fatal("create instance accepted a replaced grant")
			}
			if got := mgr.ListInstances(); len(got) != 0 {
				t.Fatalf("rejected grant published %d instances", len(got))
			}
		})
	}
}

func TestCreateInstanceRejectsBareProtectedManifest(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "repo", ".git")
	private := filepath.Join(shared, "worktrees", "task")
	workspace := filepath.Join(root, "task")
	for _, path := range []string{private, workspace} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	manifest := &config.WorkspaceIsolation{
		SharedCommonDirs: []string{shared}, WritablePaths: []string{private},
		PrivateAdminPaths: []string{private}, WritableWorkspaceRoots: []string{workspace},
	}
	if err := manifest.CaptureObjects(); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(&config.Config{Ports: config.PortConfig{Base: 0, Max: 0}}, newTestLogger(t))
	t.Cleanup(func() { _ = mgr.Shutdown(context.Background()) })
	if _, err := mgr.CreateInstance(context.Background(), &CreateRequest{WorkspacePath: workspace, WorkspaceIsolation: manifest}); err == nil {
		t.Fatal("bare protected JSON created an instance without broker-owned descriptors")
	}
	if len(mgr.ListInstances()) != 0 {
		t.Fatal("bare protected JSON published an instance")
	}
}

func TestCreateInstanceRejectsStaleControlGenerationBeforePublishing(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "repo", ".git")
	private := filepath.Join(shared, "worktrees", "task")
	workspace := filepath.Join(root, "task")
	for _, path := range []string{private, workspace} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	manifest := &config.WorkspaceIsolation{SharedCommonDirs: []string{shared}, WritablePaths: []string{private}, PrivateAdminPaths: []string{private}, WritableWorkspaceRoots: []string{workspace}}
	if err := manifest.CaptureObjects(); err != nil {
		t.Fatal(err)
	}
	pins, err := isolation.PinAll(manifest.ObjectIdentities)
	if err != nil {
		t.Fatal(err)
	}
	pin, ok := pins.Get(workspace)
	if !ok {
		t.Fatal("workspace descriptor missing")
	}
	fd := int(pin.File().Fd())
	mgr := NewManager(&config.Config{Ports: config.PortConfig{Base: 0, Max: 0}}, newTestLogger(t))
	if _, err := mgr.CreateInstanceWithPinsFenced(context.Background(), &CreateRequest{ID: "instance", WorkspacePath: workspace, WorkspaceIsolation: manifest}, pins, func(func()) bool { return false }); err == nil {
		t.Fatal("stale generation published protected instance")
	}
	if err := mgr.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(mgr.ListInstances()) != 0 {
		t.Fatal("stale generation left a published instance")
	}
	if err := unix.Fstat(fd, &unix.Stat_t{}); err != unix.EBADF {
		t.Fatalf("stale create retained workspace FD: %v", err)
	}
}

func TestCreateInstanceRejectsGrantReplacedAfterTrackerStart(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "repo", ".git")
	private := filepath.Join(shared, "worktrees", "task")
	workspace := filepath.Join(root, "task")
	for _, path := range []string{private, workspace} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	manifest := &config.WorkspaceIsolation{
		SharedCommonDirs: []string{shared}, WritablePaths: []string{private},
		PrivateAdminPaths: []string{private}, WritableWorkspaceRoots: []string{workspace},
	}
	if err := manifest.CaptureObjects(); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(&config.Config{Ports: config.PortConfig{Base: 0, Max: 0}}, newTestLogger(t))
	t.Cleanup(func() { _ = mgr.Shutdown(context.Background()) })
	mgr.afterTrackerStart = func() {
		if err := os.Rename(workspace, workspace+"-old"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(workspace, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := mgr.CreateInstance(context.Background(), &CreateRequest{
		WorkspacePath: workspace, WorkspaceIsolation: manifest,
	}); err == nil {
		t.Fatal("create instance published a grant replaced during construction")
	}
	if got := mgr.ListInstances(); len(got) != 0 {
		t.Fatalf("late replaced grant published %d instances", len(got))
	}
}

func TestCreateInstanceRejectsForgedMissingMandatoryObject(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "repo", ".git")
	private := filepath.Join(shared, "worktrees", "task")
	workspace := filepath.Join(root, "task")
	for _, path := range []string{private, workspace} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	manifest := &config.WorkspaceIsolation{
		SharedCommonDirs: []string{shared}, WritablePaths: []string{private},
		PrivateAdminPaths: []string{private}, WritableWorkspaceRoots: []string{workspace},
	}
	if err := manifest.CaptureObjects(); err != nil {
		t.Fatal(err)
	}
	for i := range manifest.ObjectIdentities {
		if manifest.ObjectIdentities[i].Path == workspace {
			manifest.ObjectIdentities[i].Exists = false
			break
		}
	}
	mgr := NewManager(&config.Config{Ports: config.PortConfig{Base: 0, Max: 0}}, newTestLogger(t))
	t.Cleanup(func() { _ = mgr.Shutdown(context.Background()) })
	if _, err := mgr.CreateInstance(context.Background(), &CreateRequest{
		WorkspacePath: workspace, WorkspaceIsolation: manifest,
	}); err == nil {
		t.Fatal("create instance accepted a missing mandatory object identity")
	}
	if got := mgr.ListInstances(); len(got) != 0 {
		t.Fatalf("forged missing grant published %d instances", len(got))
	}
}
