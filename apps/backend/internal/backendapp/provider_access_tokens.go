package backendapp

import (
	"context"

	"github.com/kandev/kandev/internal/github"
	"github.com/kandev/kandev/internal/provideraccess"
)

// registrationRerunTokens selects the exact current App registration only at
// mint time. It never uses the shared installation-token cache.
type registrationRerunTokens struct {
	clientForRegistration func(string) (provideraccess.GitHubTokenClient, error)
	revokeToken           func(context.Context, string) error
}

func newRegistrationRerunTokens(service *github.Service) registrationRerunTokens {
	return registrationRerunTokens{
		clientForRegistration: func(registrationID string) (provideraccess.GitHubTokenClient, error) {
			return service.ProviderAccessAppClient(registrationID)
		},
		revokeToken: github.RevokeInstallationToken,
	}
}

func (s registrationRerunTokens) Mint(ctx context.Context, registrationID string,
	installationID int64, canonicalRepository string) (github.InstallationToken, error) {
	if registrationID == "" || s.clientForRegistration == nil {
		return github.InstallationToken{}, provideraccess.ErrGrantUnavailable
	}
	client, err := s.clientForRegistration(registrationID)
	if err != nil || client == nil {
		return github.InstallationToken{}, provideraccess.ErrGrantUnavailable
	}
	return (provideraccess.GitHubRerunCredentials{Client: client}).Mint(
		ctx, installationID, canonicalRepository)
}

func (s registrationRerunTokens) Revoke(ctx context.Context, token string) error {
	if s.revokeToken == nil || token == "" {
		return provideraccess.ErrRevocationUnconfirmed
	}
	if err := s.revokeToken(ctx, token); err != nil {
		return provideraccess.ErrRevocationUnconfirmed
	}
	return nil
}

var _ provideraccess.RerunTokenSource = registrationRerunTokens{}
