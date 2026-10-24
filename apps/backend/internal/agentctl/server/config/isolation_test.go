package config

import "testing"

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
