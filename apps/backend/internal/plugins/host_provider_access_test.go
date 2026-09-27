package plugins

import (
	"context"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/plugins/manifest"
	"github.com/kandev/kandev/pkg/pluginsdk"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type stubProviderAccessService struct{ pluginID string }

func (s *stubProviderAccessService) Issue(_ context.Context, pluginID string,
	_ pluginsdk.ProviderAccessLeaseSpec) (pluginsdk.ProviderAccessLease, error) {
	s.pluginID = pluginID
	return pluginsdk.ProviderAccessLease{LeaseID: "lease-1", ExpiresAt: time.Now().Add(time.Minute)}, nil
}

func (s *stubProviderAccessService) Redeem(_ context.Context, pluginID, _, _ string) (pluginsdk.ProviderAccessCredential, error) {
	s.pluginID = pluginID
	return pluginsdk.NewProviderAccessCredential("fixture-bearer", time.Now().Add(time.Hour),
		"installation:42", "owner/repo", "github_actions_rerun"), nil
}

func (s *stubProviderAccessService) Release(_ context.Context, pluginID, _, _ string) (bool, error) {
	s.pluginID = pluginID
	return true, nil
}

func TestPluginHostProviderAccessRequiresCapabilityAndWiring(t *testing.T) {
	service := &stubProviderAccessService{}
	host := &pluginHost{pluginID: "coordinator", providerAccess: func() ProviderAccessService { return service }}
	manager := host.ProviderAccess()
	if _, err := manager.Issue(context.Background(), pluginsdk.ProviderAccessLeaseSpec{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("undeclared capability error = %v", err)
	}
	host.capabilities = manifest.Capabilities{APIWrite: []string{"provider_access"}}
	host.providerAccess = nil
	if _, err := manager.Redeem(context.Background(), "request-1", "lease-1"); status.Code(err) != codes.Unavailable {
		t.Fatalf("unwired service error = %v", err)
	}
	host.providerAccess = func() ProviderAccessService { return service }
	if _, err := manager.Issue(context.Background(), pluginsdk.ProviderAccessLeaseSpec{}); err != nil || service.pluginID != "coordinator" {
		t.Fatalf("connected plugin identity = %q, err = %v", service.pluginID, err)
	}
	credential, err := manager.Redeem(context.Background(), "request-2", "lease-1")
	if err != nil || credential.Bearer() != "fixture-bearer" || service.pluginID != "coordinator" {
		t.Fatalf("redemption identity = %q, err = %v", service.pluginID, err)
	}
	if _, err := manager.Release(context.Background(), "request-3", "lease-1"); err != nil || service.pluginID != "coordinator" {
		t.Fatalf("release identity = %q, err = %v", service.pluginID, err)
	}
	host.capabilities = manifest.Capabilities{}
	if _, err := manager.Redeem(context.Background(), "request-4", "lease-1"); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("removed capability redemption error = %v", err)
	}
	service.pluginID = ""
	if _, err := manager.Release(context.Background(), "request-5", "lease-1"); err != nil || service.pluginID != "coordinator" {
		t.Fatalf("removed capability release identity = %q, err = %v", service.pluginID, err)
	}
}
