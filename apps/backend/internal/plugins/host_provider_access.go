package plugins

import (
	"context"

	"github.com/kandev/kandev/pkg/pluginsdk"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ProviderAccessService is implemented by the backend application authority
// and runtime adapter. The plugin ID is stamped by this connection-bound Host.
type ProviderAccessService interface {
	Issue(context.Context, string, pluginsdk.ProviderAccessLeaseSpec) (pluginsdk.ProviderAccessLease, error)
	Redeem(context.Context, string, string, string) (pluginsdk.ProviderAccessCredential, error)
	Release(context.Context, string, string, string) (bool, error)
}

type pluginProviderAccessManager struct{ host *pluginHost }

func (h *pluginHost) ProviderAccess() pluginsdk.ProviderAccessManager {
	return &pluginProviderAccessManager{host: h}
}

func (m *pluginProviderAccessManager) resolve(requireCapability bool) (string, ProviderAccessService, error) {
	h := m.host
	if h == nil {
		return "", nil, status.Error(codes.Unavailable, "provider access is unavailable on this host")
	}
	if requireCapability && !h.capabilities.CanWrite("provider_access") {
		return "", nil, permissionDenied("provider_access")
	}
	if h.providerAccess == nil {
		return "", nil, status.Error(codes.Unavailable, "provider access is unavailable on this host")
	}
	service := h.providerAccess()
	if service == nil {
		return "", nil, status.Error(codes.Unavailable, "provider access is unavailable on this host")
	}
	return h.pluginID, service, nil
}

func (m *pluginProviderAccessManager) Issue(
	ctx context.Context, spec pluginsdk.ProviderAccessLeaseSpec,
) (pluginsdk.ProviderAccessLease, error) {
	pluginID, service, err := m.resolve(true)
	if err != nil {
		return pluginsdk.ProviderAccessLease{}, err
	}
	return service.Issue(ctx, pluginID, spec)
}

func (m *pluginProviderAccessManager) Redeem(
	ctx context.Context, requestID, leaseID string,
) (pluginsdk.ProviderAccessCredential, error) {
	pluginID, service, err := m.resolve(true)
	if err != nil {
		return pluginsdk.ProviderAccessCredential{}, err
	}
	return service.Redeem(ctx, pluginID, requestID, leaseID)
}

// Release remains reachable after capability removal so a plugin can discard
// a previously exported token; the backend service must verify lease ownership.
func (m *pluginProviderAccessManager) Release(ctx context.Context, requestID, leaseID string) (bool, error) {
	pluginID, service, err := m.resolve(false)
	if err != nil {
		return false, err
	}
	return service.Release(ctx, pluginID, requestID, leaseID)
}
