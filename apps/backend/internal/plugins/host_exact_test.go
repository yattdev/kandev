package plugins

import (
	"context"
	"reflect"
	"testing"

	"github.com/kandev/kandev/pkg/pluginsdk"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// @covers AC-1
func TestPluginHostExposesExactCapabilityContext(t *testing.T) {
	host := &pluginHost{}
	_, found := reflect.TypeOf(host).MethodByName("GetCapabilityContext")
	require.True(t, found, "the connection-bound exact capability context must be exposed by the Host")
	_, found = reflect.TypeOf((*pluginsdk.Host)(nil)).Elem().MethodByName("Exact")
	require.False(t, found, "the v1 Host interface must remain unchanged")
}

// @covers AC-1
func TestPluginHostCapabilityContextUsesBoundInstallationAndCurrentApprovals(t *testing.T) {
	host := &pluginHost{
		installationID: "installation-1",
		manifestDigest: "manifest-digest",
		exactApprovals: func(installationID string) ([]CapabilityApproval, error) {
			require.Equal(t, "installation-1", installationID)
			return []CapabilityApproval{{
				InstallationID: installationID, WorkspaceID: "workspace-1", Revision: 3, State: ApprovalStateActive,
			}}, nil
		},
	}

	capabilityContext, err := host.GetCapabilityContext(context.Background())
	require.NoError(t, err)
	require.Equal(t, pluginsdk.ExactHostContractVersion, capabilityContext.ContractVersion)
	require.Equal(t, "installation-1", capabilityContext.InstallationID)
	require.Equal(t, "manifest-digest", capabilityContext.ManifestDigest)
	require.Equal(t, []pluginsdk.CapabilityApprovalContext{{
		ApprovalID:  CanonicalApprovalDigest("capability-approval", "installation-1", "workspace-1"),
		WorkspaceID: "workspace-1", Revision: 3, Status: pluginsdk.CapabilityApprovalStatusActive,
	}}, capabilityContext.Approvals)
}

// @covers AC-1
func TestPluginHostCapabilityContextFailsClosedForMalformedApproval(t *testing.T) {
	host := &pluginHost{
		installationID: "installation-1", manifestDigest: "manifest-digest",
		exactApprovals: func(string) ([]CapabilityApproval, error) {
			return []CapabilityApproval{{InstallationID: "installation-1", WorkspaceID: "workspace-1", Revision: 3, State: "unknown"}}, nil
		},
	}

	_, err := host.GetCapabilityContext(context.Background())
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestPluginHostExactReadAuthorizationFailsClosed(t *testing.T) {
	host := &pluginHost{exactAuthorize: func(_ string, revision uint64, capabilityID, _ string) ApprovalDecision {
		return ApprovalDecision{Allowed: revision == 2 && capabilityID == "host.v2.read:workspaces"}
	}}
	require.NoError(t, host.authorizeExactRead("workspace-1", 2, "host.v2.read:workspaces", "request"))
	require.Equal(t, codes.PermissionDenied, status.Code(host.authorizeExactRead("workspace-1", 1, "host.v2.read:workspaces", "request")))
	require.Equal(t, codes.PermissionDenied, status.Code(host.authorizeExactRead("workspace-1", 2, "host.v2.read:tasks", "request")))
}
