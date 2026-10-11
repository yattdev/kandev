package repoclone

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/task/models"
)

type attachmentPathInspector interface {
	InspectHostRepositoryPath(context.Context, *models.Repository, string) (bool, error)
}

func TestManagedAttachmentPathAdmission(t *testing.T) {
	for _, scenario := range []string{"missing", "valid", "foreign", "other-workspace", "symlink-parent", "occupied", "git-file", "common-dir", "wrong-origin", "legacy", "legacy-gitlab", "legacy-gitlab-valid", "scoped", "worktree-config", "objects-link", "permission-denied", "nested-refs-link", "nested-pack-link", "nested-namespace-link", "nested-worktrees-link", "nested-loose-link", "nested-logs-link"} {
		t.Run(scenario, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			c := NewCloner(Config{BasePath: root}, ProtocolHTTPS, "", logger.Default())
			inspector, ok := any(c).(attachmentPathInspector)
			if !ok {
				t.Fatal("cloner lacks owned managed attachment admission")
			}
			repo := &models.Repository{WorkspaceID: "ws-1", SourceType: "provider", Provider: "github", ProviderHost: "https://github.com", ProviderOwner: "acme", ProviderName: "notes", RemoteURL: "https://github.com/acme/notes.git"}
			if scenario == "legacy-gitlab" {
				repo.Provider = "gitlab"
				repo.ProviderHost = "https://gitlab.example"
				repo.RemoteURL = "https://gitlab.example/acme/notes.git"
			}
			if scenario == "scoped" {
				repo.Provider = "fixture"
				repo.ProviderScope = "connection"
				repo.ProviderRepoID = "123"
			}
			path, err := c.WorkspaceProviderRepositoryPath(repo.WorkspaceID, repo.Provider, repo.ProviderHost, repo.ProviderScope, repo.ProviderRepoID, repo.ProviderOwner, repo.ProviderName)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "foreign":
				path = filepath.Join(t.TempDir(), "notes")
			case "other-workspace":
				path, err = c.WorkspaceRepoPath("other", "github", "acme", "notes")
				if err != nil {
					t.Fatal(err)
				}
			case "legacy":
				path, err = c.ProviderRepoPath("github", repo.ProviderHost, "acme", "notes")
				if err != nil {
					t.Fatal(err)
				}
			case "legacy-gitlab", "legacy-gitlab-valid":
				path, err = c.RepoPath("acme", "notes")
				if err != nil {
					t.Fatal(err)
				}
			case "symlink-parent":
				if err := os.MkdirAll(filepath.Dir(filepath.Dir(path)), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), filepath.Dir(path)); err != nil {
					t.Fatal(err)
				}
			}
			nestedLink := scenario == "nested-refs-link" || scenario == "nested-pack-link" || scenario == "nested-namespace-link" || scenario == "nested-worktrees-link" || scenario == "nested-loose-link" || scenario == "nested-logs-link"
			exists := nestedLink || scenario == "legacy-gitlab-valid" || scenario == "valid" || scenario == "wrong-origin" || scenario == "common-dir" || scenario == "worktree-config" || scenario == "objects-link"
			if exists {
				runGit(t, root, "init", path)
				origin := repo.RemoteURL
				if scenario == "wrong-origin" {
					origin = "https://github.com/foreign/notes.git"
				}
				runGit(t, path, "remote", "add", "origin", origin)
				if scenario == "common-dir" {
					if err := os.WriteFile(filepath.Join(path, ".git", "commondir"), []byte(t.TempDir()), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if scenario == "worktree-config" {
				runGit(t, path, "config", "core.worktree", t.TempDir())
			}
			if scenario == "objects-link" {
				objects := filepath.Join(path, ".git", "objects")
				// Replace only the empty test repository's object directory.
				if err := os.Rename(objects, objects+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), objects); err != nil {
					t.Fatal(err)
				}
			}
			if nestedLink {
				seedNestedAttachmentMetadataLink(t, path, scenario)
			}
			if scenario == "occupied" || scenario == "git-file" {
				if err := os.MkdirAll(path, 0755); err != nil {
					t.Fatal(err)
				}
				if scenario == "git-file" {
					if err := os.WriteFile(filepath.Join(path, ".git"), []byte("gitdir: /foreign\n"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if scenario == "permission-denied" {
				if os.Geteuid() == 0 || runtime.GOOS == "windows" {
					t.Skip("permission denial requires a non-root POSIX account")
				}
				parent := filepath.Dir(path)
				if err := os.MkdirAll(parent, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(parent, 0000); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := os.Chmod(parent, 0755); err != nil {
						t.Error(err)
					}
				})
			}
			found, err := inspector.InspectHostRepositoryPath(context.Background(), repo, path)
			switch scenario {
			case "missing", "legacy", "legacy-gitlab", "scoped":
				if err != nil || found {
					t.Fatalf("owned absent path: %v %v", found, err)
				}
			case "valid", "legacy-gitlab-valid":
				if err != nil || !found {
					t.Fatalf("valid checkout: %v %v", found, err)
				}
			default:
				if err == nil {
					t.Fatalf("unsafe %s path admitted", scenario)
				}
			}
			if scenario == "permission-denied" && !errors.Is(err, os.ErrPermission) {
				t.Fatalf("permission failure=%v", err)
			}
			if scenario == "foreign" && !errors.Is(err, ErrRepositoryOwnershipMismatch) {
				t.Fatalf("foreign path error=%v", err)
			}
		})
	}
}

func seedNestedAttachmentMetadataLink(t *testing.T, path, scenario string) {
	t.Helper()
	metadata := map[string]string{"nested-refs-link": "refs/heads", "nested-pack-link": "objects/pack", "nested-namespace-link": "refs/heads/team", "nested-worktrees-link": "worktrees", "nested-loose-link": "objects/ab", "nested-logs-link": "logs/refs"}[scenario]
	target := filepath.Join(path, ".git", filepath.FromSlash(metadata))
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(target); err == nil {
		if err := os.Rename(target, target+".original"); err != nil {
			t.Fatal(err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), target); err != nil {
		t.Fatal(err)
	}
}
