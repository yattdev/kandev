package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWorkspaceIsolationBindsEveryGrantObject(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("object pinning requires Linux")
	}
	root := t.TempDir()
	shared := filepath.Join(root, "shared", ".git")
	private := filepath.Join(shared, "worktrees", "task")
	workspace := filepath.Join(root, "task")
	for _, path := range []string{private, workspace} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	manifest := WorkspaceIsolation{
		SharedCommonDirs:       []string{shared},
		WritablePaths:          []string{private},
		PrivateAdminPaths:      []string{private},
		WritableWorkspaceRoots: []string{workspace},
	}
	if err := manifest.CaptureObjects(); err != nil {
		t.Fatal(err)
	}
	if err := manifest.ValidateObjects(); err != nil {
		t.Fatalf("valid object binding rejected: %v", err)
	}
	manifest.WritablePaths = append(manifest.WritablePaths, filepath.Join(private, "new.lock"))
	if err := manifest.ValidateObjects(); err == nil {
		t.Fatal("manifest accepted writable grant without an object binding")
	}
}

func TestWorkspaceIsolationRejectsMissingMandatoryDirectory(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("object pinning requires Linux")
	}
	for _, missing := range []string{"shared", "private", "objects", "workspace"} {
		t.Run(missing, func(t *testing.T) {
			root := t.TempDir()
			shared := filepath.Join(root, "repo", ".git")
			private := filepath.Join(shared, "worktrees", "task")
			objects := filepath.Join(private, "kandev-agent-git", "objects")
			workspace := filepath.Join(root, "task")
			for _, path := range []string{objects, workspace} {
				if err := os.MkdirAll(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			manifest := WorkspaceIsolation{
				SharedCommonDirs:       []string{shared},
				WritablePaths:          []string{private, objects},
				PrivateAdminPaths:      []string{private},
				WritableWorkspaceRoots: []string{workspace},
				RequiredDirectories:    []string{objects},
			}
			paths := map[string]string{"shared": shared, "private": private, "objects": objects, "workspace": workspace}
			if err := os.RemoveAll(paths[missing]); err != nil {
				t.Fatal(err)
			}
			if err := manifest.CaptureObjects(); err == nil {
				t.Fatalf("captured missing %s directory as a grant", missing)
			}
		})
	}
}

func TestWorkspaceIsolationConfirmsMandatoryObjectAfterValidation(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("object pinning requires Linux")
	}
	root := t.TempDir()
	shared := filepath.Join(root, "repo", ".git")
	private := filepath.Join(shared, "worktrees", "task")
	workspace := filepath.Join(root, "task")
	for _, path := range []string{private, workspace} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	manifest := WorkspaceIsolation{
		SharedCommonDirs: []string{shared}, WritablePaths: []string{private},
		PrivateAdminPaths: []string{private}, WritableWorkspaceRoots: []string{workspace},
	}
	if err := manifest.CaptureObjects(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(private, private+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(private, 0700); err != nil {
		t.Fatal(err)
	}
	if err := manifest.ConfirmObjects(); err == nil {
		t.Fatal("confirmed a private directory replaced during validation")
	}
}

func TestWorkspaceIsolationRejectsRegularFileWorkspace(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("object pinning requires Linux")
	}
	root := t.TempDir()
	shared := filepath.Join(root, "repo", ".git")
	private := filepath.Join(shared, "worktrees", "task")
	workspace := filepath.Join(root, "task")
	if err := os.MkdirAll(private, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workspace, nil, 0600); err != nil {
		t.Fatal(err)
	}
	manifest := WorkspaceIsolation{
		SharedCommonDirs: []string{shared}, WritablePaths: []string{private},
		PrivateAdminPaths: []string{private}, WritableWorkspaceRoots: []string{workspace},
	}
	if err := manifest.CaptureObjects(); err == nil {
		t.Fatal("captured a regular file as writable workspace directory")
	}
}

func TestWorkspaceIsolationRejectsWritableSharedCommonDirectory(t *testing.T) {
	manifest := WorkspaceIsolation{SharedCommonDirs: []string{"/repo/.git"}, WritablePaths: []string{"/repo/.git/refs"}, WritableWorkspaceRoots: []string{"/task"}}
	if err := manifest.Validate(); err == nil {
		t.Fatal("Validate() accepted a writable path within a shared common directory")
	}
}

func TestWorkspaceIsolationPermitsValidatedPrivateAdminPath(t *testing.T) {
	manifest := WorkspaceIsolation{SharedCommonDirs: []string{"/repo/.git"}, WritablePaths: []string{"/repo/.git/worktrees/task"}, PrivateAdminPaths: []string{"/repo/.git/worktrees/task"}, WritableWorkspaceRoots: []string{"/task"}}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
}

func TestWorkspaceIsolationRejectsForgedSharedDirectoryException(t *testing.T) {
	manifest := WorkspaceIsolation{
		SharedCommonDirs:       []string{"/repo/.git"},
		WritablePaths:          []string{"/repo/.git/refs/heads/main"},
		PrivateAdminPaths:      []string{"/repo/.git/refs/heads/main"},
		WritableWorkspaceRoots: []string{"/task"},
	}
	if err := manifest.Validate(); err == nil {
		t.Fatal("Validate() accepted a forged exception inside shared refs")
	}
}

func TestWorkspaceIsolationRejectsMalformedPaths(t *testing.T) {
	for _, manifest := range []WorkspaceIsolation{
		{WritablePaths: []string{"/tmp/worktree"}},
		{SharedCommonDirs: []string{"repo/.git"}, WritablePaths: []string{"repo/.git/worktrees/task"}, PrivateAdminPaths: []string{"repo/.git/worktrees/task"}, WritableWorkspaceRoots: []string{"/task"}},
		{SharedCommonDirs: []string{"/repo/.git/.."}, WritablePaths: []string{"/repo/.git/worktrees/task"}, PrivateAdminPaths: []string{"/repo/.git/worktrees/task"}, WritableWorkspaceRoots: []string{"/task"}},
	} {
		if err := manifest.Validate(); err == nil {
			t.Fatalf("Validate() accepted malformed manifest %+v", manifest)
		}
	}
}

func TestWorkspaceIsolationRejectsWritableAncestor(t *testing.T) {
	manifest := WorkspaceIsolation{SharedCommonDirs: []string{"/repo/.git"}, WritablePaths: []string{"/repo"}, WritableWorkspaceRoots: []string{"/task"}}
	if err := manifest.Validate(); err == nil {
		t.Fatal("Validate() accepted a writable ancestor of shared Git metadata")
	}
}

func TestWorkspaceIsolationRejectsRootAndNULGrants(t *testing.T) {
	for _, path := range []string{"/", "/tmp/invalid\x00path"} {
		manifest := WorkspaceIsolation{
			SharedCommonDirs:       []string{"/repo/.git"},
			WritablePaths:          []string{path},
			WritableWorkspaceRoots: []string{"/task"},
		}
		if err := manifest.Validate(); err == nil {
			t.Fatalf("Validate() accepted malformed writable grant %q", path)
		}
	}
}

func TestWorkspaceIsolationRequiresWritableWorkspaceRoot(t *testing.T) {
	manifest := WorkspaceIsolation{
		SharedCommonDirs:  []string{"/repo/.git"},
		WritablePaths:     []string{"/repo/.git/worktrees/task"},
		PrivateAdminPaths: []string{"/repo/.git/worktrees/task"},
	}
	if err := manifest.Validate(); err == nil {
		t.Fatal("Validate() accepted a manifest without a writable task workspace root")
	}
}

func TestWorkspaceIsolationRejectsRootSharedCommonDirectory(t *testing.T) {
	manifest := WorkspaceIsolation{
		SharedCommonDirs:       []string{"/"},
		WritablePaths:          []string{"/worktrees/task"},
		PrivateAdminPaths:      []string{"/worktrees/task"},
		WritableWorkspaceRoots: []string{"/task"},
	}
	if err := manifest.Validate(); err == nil {
		t.Fatal("Validate() accepted filesystem root as a shared Git common directory")
	}
}
