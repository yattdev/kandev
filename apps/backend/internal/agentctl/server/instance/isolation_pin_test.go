//go:build linux

package instance

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kandev/kandev/internal/agentctl/server/config"
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
