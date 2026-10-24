package config

import "testing"

func TestWorkspaceIsolationRejectsWritableSharedCommonDirectory(t *testing.T) {
	manifest := WorkspaceIsolation{SharedCommonDirs: []string{"/repo/.git"}, WritablePaths: []string{"/repo/.git/refs"}}
	if err := manifest.Validate(); err == nil {
		t.Fatal("Validate() accepted a writable path within a shared common directory")
	}
}

func TestWorkspaceIsolationPermitsValidatedPrivateAdminPath(t *testing.T) {
	manifest := WorkspaceIsolation{SharedCommonDirs: []string{"/repo/.git"}, WritablePaths: []string{"/repo/.git/worktrees/task"}, PrivateAdminPaths: []string{"/repo/.git/worktrees/task"}}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
}

func TestWorkspaceIsolationRejectsForgedSharedDirectoryException(t *testing.T) {
	manifest := WorkspaceIsolation{
		SharedCommonDirs:  []string{"/repo/.git"},
		WritablePaths:     []string{"/repo/.git/refs/heads/main"},
		PrivateAdminPaths: []string{"/repo/.git/refs/heads/main"},
	}
	if err := manifest.Validate(); err == nil {
		t.Fatal("Validate() accepted a forged exception inside shared refs")
	}
}

func TestWorkspaceIsolationRejectsMalformedPaths(t *testing.T) {
	for _, manifest := range []WorkspaceIsolation{
		{WritablePaths: []string{"/tmp/worktree"}},
		{SharedCommonDirs: []string{"repo/.git"}, WritablePaths: []string{"repo/.git/worktrees/task"}, PrivateAdminPaths: []string{"repo/.git/worktrees/task"}},
		{SharedCommonDirs: []string{"/repo/.git/.."}, WritablePaths: []string{"/repo/.git/worktrees/task"}, PrivateAdminPaths: []string{"/repo/.git/worktrees/task"}},
	} {
		if err := manifest.Validate(); err == nil {
			t.Fatalf("Validate() accepted malformed manifest %+v", manifest)
		}
	}
}

func TestWorkspaceIsolationRejectsWritableAncestor(t *testing.T) {
	manifest := WorkspaceIsolation{SharedCommonDirs: []string{"/repo/.git"}, WritablePaths: []string{"/repo"}}
	if err := manifest.Validate(); err == nil {
		t.Fatal("Validate() accepted a writable ancestor of shared Git metadata")
	}
}
