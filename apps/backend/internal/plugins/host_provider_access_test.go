package plugins

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/plugins/manifest"
	"github.com/kandev/kandev/pkg/pluginsdk"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type stubProviderAccessService struct {
	pluginID string
	stops    []string
	closed   bool
	stopErr  error
}

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

func (s *stubProviderAccessService) StopPlugin(_ context.Context, pluginID string) error {
	s.stops = append(s.stops, pluginID)
	return s.stopErr
}

func TestPluginServiceStopsProviderAccessOnLifecycleTransitions(t *testing.T) {
	svc, _, _ := newTestService(t)
	installTestPlugin(t, svc, "kandev-plugin-slack")
	provider := &stubProviderAccessService{}
	svc.SetProviderAccess(provider)
	if err := svc.Disable("kandev-plugin-slack"); err != nil {
		t.Fatal(err)
	}
	if len(provider.stops) != 1 || provider.stops[0] != "kandev-plugin-slack" {
		t.Fatalf("disable provider stops = %v", provider.stops)
	}
	if err := svc.Uninstall(context.Background(), "kandev-plugin-slack"); err != nil {
		t.Fatal(err)
	}
	if len(provider.stops) != 2 || provider.stops[1] != "kandev-plugin-slack" {
		t.Fatalf("uninstall provider stops = %v", provider.stops)
	}
	svc.Shutdown()
	if !provider.closed {
		t.Fatal("provider access runtime not closed at shutdown")
	}
}

func TestPluginServiceRetriesFailedProviderStopOnDisabledPlugin(t *testing.T) {
	svc, _, _ := newTestService(t)
	installTestPlugin(t, svc, "kandev-plugin-slack")
	provider := &stubProviderAccessService{stopErr: errors.New("provider revocation failed")}
	svc.SetProviderAccess(provider)
	if err := svc.Disable("kandev-plugin-slack"); !errors.Is(err, provider.stopErr) {
		t.Fatalf("disable error = %v", err)
	}
	provider.stopErr = nil
	if err := svc.Disable("kandev-plugin-slack"); err != nil {
		t.Fatalf("repeat disable error = %v", err)
	}
	if len(provider.stops) != 2 {
		t.Fatalf("provider stop attempts = %d", len(provider.stops))
	}
}

func (s *stubProviderAccessService) Stop(context.Context) error {
	s.closed = true
	return nil
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
