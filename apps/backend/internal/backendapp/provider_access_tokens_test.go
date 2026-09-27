package backendapp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/github"
	"github.com/kandev/kandev/internal/provideraccess"
)

type scopedAppClientStub struct {
	mints      int
	installID  int64
	permission github.InstallationPermissions
	repos      []string
}

func (s *scopedAppClientStub) MintInstallationToken(_ context.Context, installationID int64,
	permissions github.InstallationPermissions, repositories []string) (github.InstallationToken, error) {
	s.mints++
	s.installID = installationID
	s.permission = permissions
	s.repos = repositories
	return github.InstallationToken{Token: "fixture-bearer", ExpiresAt: time.Now().Add(time.Hour),
		Principal: github.TokenPrincipal{Kind: github.TokenCredentialInstallation,
			PrincipalID: "installation:42", InstallationID: 42},
		Permissions:  permissions,
		Repositories: []github.InstallationTokenRepository{{FullName: "owner/base"}}}, nil
}

func (s *scopedAppClientStub) RevokeInstallationToken(context.Context, string) error { return nil }

func TestRegistrationRerunTokensSelectsExactAppAndNeverCachesMint(t *testing.T) {
	client := &scopedAppClientStub{}
	selected := ""
	revoked := ""
	source := registrationRerunTokens{
		clientForRegistration: func(registrationID string) (provideraccess.GitHubTokenClient, error) {
			selected = registrationID
			if registrationID != "registration-1" {
				return nil, errors.New("unknown App registration")
			}
			return client, nil
		},
		revokeToken: func(_ context.Context, token string) error { revoked = token; return nil },
	}
	ctx := context.Background()
	if _, err := source.Mint(ctx, "foreign-registration", 42, "owner/base"); !errors.Is(err, provideraccess.ErrGrantUnavailable) {
		t.Fatalf("foreign registration mint = %v", err)
	}
	for range 2 {
		if _, err := source.Mint(ctx, "registration-1", 42, "owner/base"); err != nil {
			t.Fatal(err)
		}
	}
	if selected != "registration-1" || client.mints != 2 || client.installID != 42 ||
		len(client.repos) != 1 || client.repos[0] != "base" ||
		client.permission["actions"] != github.PermissionWrite || len(client.permission) != 3 {
		t.Fatalf("App selection = %q, mints = %d, installation = %d, repos = %v, permissions = %v",
			selected, client.mints, client.installID, client.repos, client.permission)
	}
	if err := source.Revoke(ctx, "fixture-bearer"); err != nil || revoked != "fixture-bearer" {
		t.Fatalf("exact token revocation = %q, %v", revoked, err)
	}
}
