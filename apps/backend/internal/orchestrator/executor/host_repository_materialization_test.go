package executor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/repoclone"
	"github.com/kandev/kandev/internal/task/models"
)

type attachmentCloneFixture struct {
	*repoclone.Cloner
	t          *testing.T
	origin     string
	calls      []repoclone.GitCredentialRequest
	cloneErr   error
	resultMode string
}

func (c *attachmentCloneFixture) EnsureWorkspaceClonedWithCredentialRequestAndState(_ context.Context, request repoclone.GitCredentialRequest, _, _ string) (string, repoclone.RemoteRefState, error) {
	c.calls = append(c.calls, request)
	if c.resultMode == "empty-result" {
		return "", repoclone.RemoteRefStateUnknown, nil
	}
	if c.resultMode == "foreign-result" {
		return "/foreign/checkout", repoclone.RemoteRefStateUnknown, nil
	}
	if c.cloneErr != nil {
		return "", repoclone.RemoteRefStateUnknown, c.cloneErr
	}
	path, err := c.WorkspaceProviderRepositoryPath(request.WorkspaceID, request.Provider, request.ProviderHost, request.ProviderScope, request.ProviderRepositoryID, request.Owner, request.Name)
	if err != nil {
		return "", repoclone.RemoteRefStateUnknown, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", repoclone.RemoteRefStateUnknown, err
	}
	runGitInTest(c.t, "", "clone", c.origin, path)
	return path, repoclone.RemoteRefStateHasRefs, nil
}

type attachmentPathUpdater struct {
	recordingRepoUpdater
	path string
	err  error
}

func (u *attachmentPathUpdater) UpdateRepositoryLocalPath(_ context.Context, _, path string) error {
	if u.err != nil {
		return u.err
	}
	u.path = path
	return nil
}

func TestHostRepositoryMaterializationManagedPaths(t *testing.T) {
	for _, saved := range []string{"missing", "empty", "foreign", "local", "persist-failure", "auth-failure", "empty-result", "foreign-result", "canceled", "missing-session"} {
		t.Run(saved, func(t *testing.T) {
			origin := initBareOriginWithMain(t)
			cloner := &attachmentCloneFixture{Cloner: repoclone.NewCloner(repoclone.Config{BasePath: t.TempDir()}, repoclone.ProtocolHTTPS, "", logger.Default()), origin: origin, t: t}
			repository := &models.Repository{ID: "notes", WorkspaceID: "ws-1", SourceType: "provider", Provider: "fixture", ProviderHost: "https://fixture.example", ProviderOwner: "acme", ProviderName: "notes", RemoteURL: origin}
			path, err := cloner.WorkspaceProviderRepositoryPath("ws-1", "fixture", repository.ProviderHost, "", "", "acme", "notes")
			if err != nil {
				t.Fatal(err)
			}
			if saved != "empty" && saved != "persist-failure" {
				repository.LocalPath = path
			}
			if saved == "foreign" || saved == "local" {
				repository.LocalPath = filepath.Join(t.TempDir(), "user-owned")
			}
			if saved == "local" {
				repository.SourceType = "local"
			}
			updater := &attachmentPathUpdater{}
			if saved == "persist-failure" {
				updater.err = errors.New("persistence unavailable")
			}
			if saved == "auth-failure" {
				cloner.cloneErr = repoclone.ErrWorkspaceCredentialUnavailable
			}
			exc := newTestExecutor(t, &mockAgentManager{}, newMockRepository())
			exc.SetRepoCloner(cloner, updater)
			ctx := context.Background()
			if saved == "empty-result" || saved == "foreign-result" {
				cloner.resultMode = saved
			}
			if saved == "canceled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			sessionID := "session-1"
			if saved == "missing-session" {
				sessionID = ""
			}
			result, err := exc.EnsureRepositoryClonedForSession(ctx, "task-1", sessionID, repository)
			switch saved {
			case "foreign", "persist-failure", "auth-failure", "empty-result", "foreign-result", "canceled", "missing-session":
				if err == nil {
					t.Fatalf("%s must fail, got path %q", saved, result)
				}
				if saved == "foreign" && len(cloner.calls) != 0 {
					t.Fatal("foreign path caused cloning")
				}
			case "local":
				if err != nil || result != repository.LocalPath || len(cloner.calls) != 0 {
					t.Fatalf("local source redirected: %q %v", result, err)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
				if _, err := os.ReadFile(filepath.Join(result, "README.md")); err != nil {
					t.Fatalf("managed checkout missing: %v", err)
				}
				if saved == "empty" && updater.path != result {
					t.Fatalf("path not persisted: %q vs %q", updater.path, result)
				}
				if len(cloner.calls) != 1 || cloner.calls[0].TaskID != "task-1" || cloner.calls[0].SessionID != "session-1" {
					t.Fatalf("wrong scope: %+v", cloner.calls)
				}
			}
		})
	}
}

type attachmentDeniedCredentials struct {
	calls []repoclone.GitCredentialRequest
}

func (c *attachmentDeniedCredentials) ResolveGitCredential(_ context.Context, request repoclone.GitCredentialRequest) (string, string, error) {
	c.calls = append(c.calls, request)
	return "", "", repoclone.ErrWorkspaceCredentialUnavailable
}

func TestHostRepositoryMaterializationCredentialDenial(t *testing.T) {
	cloner := repoclone.NewCloner(repoclone.Config{BasePath: t.TempDir()}, repoclone.ProtocolHTTPS, "", logger.Default())
	credentials := &attachmentDeniedCredentials{}
	cloner.SetGitCredentialProvider(credentials)
	exc := newTestExecutor(t, &mockAgentManager{}, newMockRepository())
	exc.SetRepoCloner(cloner, &attachmentPathUpdater{})
	repo := &models.Repository{ID: "notes", WorkspaceID: "ws-1", SourceType: "provider", Provider: "github", ProviderHost: "https://github.com", ProviderOwner: "acme", ProviderName: "notes"}
	_, err := exc.EnsureRepositoryClonedForSession(context.Background(), "task-1", "session-1", repo)
	if !errors.Is(err, repoclone.ErrWorkspaceCredentialUnavailable) {
		t.Fatalf("credential error=%v", err)
	}
	if len(credentials.calls) != 1 {
		t.Fatalf("credential requests=%+v", credentials.calls)
	}
	request := credentials.calls[0]
	if request.TaskID != "task-1" || request.SessionID != "session-1" || request.RepositoryID != "notes" || request.WorkspaceID != "ws-1" {
		t.Fatalf("credential scope=%+v", request)
	}
	if repo.LocalPath != "" {
		t.Fatal("denied clone populated local path")
	}
}
