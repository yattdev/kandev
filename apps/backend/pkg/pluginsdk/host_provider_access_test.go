package pluginsdk

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	pluginv1 "github.com/kandev/kandev/proto/kandev/plugin/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/descriptorpb"
)

type recordingProviderAccessManager struct {
	spec            ProviderAccessLeaseSpec
	redeemedLeaseID string
	releasedLeaseID string
}

func TestProviderAccessBearerDebugFormattingIsRedacted(t *testing.T) {
	const secret = "credential-secret-sentinel"
	protoResponse := &pluginv1.RedeemProviderAccessLeaseExactResponse{Token: secret}
	field := protoResponse.ProtoReflect().Descriptor().Fields().ByName("token")
	options, ok := field.Options().(*descriptorpb.FieldOptions)
	if !ok || !options.GetDebugRedact() {
		t.Fatal("protobuf bearer field lacks debug-redaction metadata")
	}
	credential := ProviderAccessCredential{token: secret}
	if strings.Contains(fmt.Sprintf("%v", credential), secret) ||
		strings.Contains(fmt.Sprintf("%#v", credential), secret) {
		t.Fatal("SDK debug formatting exposed provider bearer")
	}
	encoded, err := json.Marshal(credential)
	if err != nil || strings.Contains(string(encoded), secret) {
		t.Fatalf("SDK JSON formatting exposed provider bearer: %v", err)
	}
	if credential.Bearer() != secret {
		t.Fatal("explicit bearer accessor lost credential")
	}
}

func (m *recordingProviderAccessManager) Issue(_ context.Context, spec ProviderAccessLeaseSpec) (ProviderAccessLease, error) {
	m.spec = spec
	return ProviderAccessLease{LeaseID: "lease-1", ExpiresAt: time.Date(2026, 9, 27, 6, 0, 0, 0, time.UTC),
		TargetDigest: "digest", GrantGeneration: 2, ApprovalRevision: 3,
		ConnectionGeneration: "connection", CanonicalRepository: "owner/repo", Target: spec.Target}, nil
}

func (m *recordingProviderAccessManager) Redeem(_ context.Context, _, leaseID string) (ProviderAccessCredential, error) {
	m.redeemedLeaseID = leaseID
	return ProviderAccessCredential{token: "transient-fixture", ExpiresAt: time.Date(2026, 9, 27, 7, 0, 0, 0, time.UTC),
		ProviderPrincipalID: "installation:42", CanonicalRepository: "owner/repo",
		PermissionProfile: "github_actions_rerun"}, nil
}

func (m *recordingProviderAccessManager) Release(_ context.Context, _, leaseID string) (bool, error) {
	m.releasedLeaseID = leaseID
	return true, nil
}

type providerAccessRecordingHost struct {
	recordingHost
	manager ProviderAccessManager
}

func (h *providerAccessRecordingHost) ProviderAccess() ProviderAccessManager { return h.manager }

func TestHostProviderAccessV1RoundTripAndDisabledDefault(t *testing.T) {
	manager, ok := ProviderAccess(dialHostOverBufconn(t, &recordingHost{}))
	if !ok {
		t.Fatal("plugin client lacks provider access extension")
	}
	if _, err := manager.Redeem(context.Background(), "request-1", "lease-1"); status.Code(err) != codes.Unimplemented {
		t.Fatalf("disabled Host redemption = %v", err)
	}

	recorder := &recordingProviderAccessManager{}
	manager, ok = ProviderAccess(dialHostOverBufconn(t, &providerAccessRecordingHost{manager: recorder}))
	if !ok {
		t.Fatal("plugin client lacks provider access extension")
	}
	target := ProviderAccessGitHubRerunTarget{Operation: "rerun_failed_jobs", PRNumber: 3165,
		BaseRepositoryID: 10, BaseRepository: "owner/repo", BaseRef: "main", BaseSHA: "a",
		HeadRepositoryID: 20, HeadRepository: "contributor/fork", HeadRef: "feature", HeadSHA: "b",
		SourceRunID: 77, SourceAttempt: 2, WorkflowID: 55}
	spec := ProviderAccessLeaseSpec{RequestID: "request-1", IdempotencyKey: "idem-1", GrantID: "grant-1",
		WorkspaceID: "workspace-1", ManagedTaskID: "managed-1", SessionID: "session-1",
		TargetTaskID: "target-1", RepositoryID: "repo-row-1", Provider: "github", Purpose: "actions_write",
		Target: target}
	lease, err := manager.Issue(context.Background(), spec)
	if err != nil || lease.LeaseID != "lease-1" || recorder.spec != spec || lease.Target != target {
		t.Fatalf("lease = %+v, recorded = %+v, err = %v", lease, recorder.spec, err)
	}
	credential, err := manager.Redeem(context.Background(), "request-2", lease.LeaseID)
	if err != nil || credential.Bearer() != "transient-fixture" || recorder.redeemedLeaseID != lease.LeaseID {
		t.Fatalf("credential = %+v, redeemed = %q, err = %v", credential, recorder.redeemedLeaseID, err)
	}
	revoked, err := manager.Release(context.Background(), "request-3", lease.LeaseID)
	if err != nil || !revoked || recorder.releasedLeaseID != lease.LeaseID {
		t.Fatalf("release = %v, %q, err = %v", revoked, recorder.releasedLeaseID, err)
	}
}

func TestHostProviderAccessV1RejectsIncompleteWireBeforeManager(t *testing.T) {
	recorder := &recordingProviderAccessManager{}
	server := &grpcHostServer{impl: &providerAccessRecordingHost{manager: recorder}}
	for _, request := range []*pluginv1.IssueProviderAccessLeaseExactRequest{
		{ApiVersion: "provider-access/v2", RequestId: "request-1"},
		{ApiVersion: ProviderAccessV1, RequestId: "request-1"},
	} {
		if _, err := server.IssueProviderAccessLeaseExact(context.Background(), request); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("incomplete wire error = %v", err)
		}
	}
	if recorder.spec.RequestID != "" {
		t.Fatal("invalid wire reached provider access manager")
	}
}
