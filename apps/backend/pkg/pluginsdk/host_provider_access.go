package pluginsdk

import (
	"context"
	"time"

	pluginv1 "github.com/kandev/kandev/proto/kandev/plugin/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const ProviderAccessV1 = "provider-access/v1"

// ProviderAccessHost is an optional connection-bound Host extension.
type ProviderAccessHost interface {
	ProviderAccess() ProviderAccessManager
}

// ProviderAccess returns the optional direct-provider credential lease API.
func ProviderAccess(host Host) (ProviderAccessManager, bool) {
	extension, ok := host.(ProviderAccessHost)
	if !ok {
		return nil, false
	}
	return extension.ProviderAccess(), true
}

// ProviderAccessManager issues one exact lease, redeems one transient bearer,
// and releases that same bearer. It never performs a provider operation.
type ProviderAccessManager interface {
	Issue(context.Context, ProviderAccessLeaseSpec) (ProviderAccessLease, error)
	Redeem(context.Context, string, string) (ProviderAccessCredential, error)
	Release(context.Context, string, string) (bool, error)
}

type ProviderAccessGitHubRerunTarget struct {
	Operation        string
	PRNumber         int32
	BaseRepositoryID int64
	BaseRepository   string
	BaseRef          string
	BaseSHA          string
	HeadRepositoryID int64
	HeadRepository   string
	HeadRef          string
	HeadSHA          string
	SourceRunID      int64
	SourceAttempt    int32
	WorkflowID       int64
}

type ProviderAccessLeaseSpec struct {
	RequestID      string
	IdempotencyKey string
	GrantID        string
	WorkspaceID    string
	ManagedTaskID  string
	SessionID      string
	TargetTaskID   string
	RepositoryID   string
	Provider       string
	Purpose        string
	Target         ProviderAccessGitHubRerunTarget
}

type ProviderAccessLease struct {
	LeaseID              string
	ExpiresAt            time.Time
	TargetDigest         string
	GrantGeneration      int64
	ApprovalRevision     uint64
	ConnectionGeneration string
	CanonicalRepository  string
	Target               ProviderAccessGitHubRerunTarget
}

// ProviderAccessCredential contains transient bearer material. A plugin must
// keep it in memory only and call Release when its provider call is finished.
type ProviderAccessCredential struct {
	token               string
	ExpiresAt           time.Time
	ProviderPrincipalID string
	CanonicalRepository string
	PermissionProfile   string
}

// NewProviderAccessCredential is for the Host-side adapter only. It keeps the
// bearer out of exported struct fields while the SDK can return it explicitly.
func NewProviderAccessCredential(
	token string, expiresAt time.Time, principalID, repository, profile string,
) ProviderAccessCredential {
	return ProviderAccessCredential{token: token, ExpiresAt: expiresAt,
		ProviderPrincipalID: principalID, CanonicalRepository: repository,
		PermissionProfile: profile}
}

// Bearer deliberately requires an explicit call so ordinary formatting and
// JSON serialization cannot accidentally include the transient credential.
func (credential ProviderAccessCredential) Bearer() string { return credential.token }

func (ProviderAccessCredential) String() string { return "ProviderAccessCredential{Bearer:[REDACTED]}" }

func (credential ProviderAccessCredential) GoString() string { return credential.String() }

func (h *grpcHostClient) ProviderAccess() ProviderAccessManager {
	return grpcProviderAccessManager{client: h.client}
}

type grpcProviderAccessManager struct{ client pluginv1.HostClient }

func (m grpcProviderAccessManager) Issue(ctx context.Context, spec ProviderAccessLeaseSpec) (ProviderAccessLease, error) {
	resp, err := m.client.IssueProviderAccessLeaseExact(ctx, spec.toProto())
	if err != nil {
		return ProviderAccessLease{}, err
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, resp.GetExpiresAt())
	if err != nil {
		return ProviderAccessLease{}, status.Error(codes.Internal, "invalid provider access lease expiry")
	}
	return ProviderAccessLease{LeaseID: resp.GetLeaseId(), ExpiresAt: expiresAt,
		TargetDigest: resp.GetTargetDigest(), GrantGeneration: resp.GetGrantGeneration(),
		ApprovalRevision: resp.GetApprovalRevision(), ConnectionGeneration: resp.GetConnectionGeneration(),
		CanonicalRepository: resp.GetCanonicalRepository(), Target: targetFromProto(resp.GetTarget())}, nil
}

func (m grpcProviderAccessManager) Redeem(ctx context.Context, requestID, leaseID string) (ProviderAccessCredential, error) {
	resp, err := m.client.RedeemProviderAccessLeaseExact(ctx, &pluginv1.RedeemProviderAccessLeaseExactRequest{
		ApiVersion: ProviderAccessV1, RequestId: requestID, LeaseId: leaseID})
	if err != nil {
		return ProviderAccessCredential{}, err
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, resp.GetExpiresAt())
	if err != nil {
		return ProviderAccessCredential{}, status.Error(codes.Internal, "invalid provider access credential expiry")
	}
	return ProviderAccessCredential{token: resp.GetToken(), ExpiresAt: expiresAt,
		ProviderPrincipalID: resp.GetProviderPrincipalId(), CanonicalRepository: resp.GetCanonicalRepository(),
		PermissionProfile: resp.GetPermissionProfile()}, nil
}

func (m grpcProviderAccessManager) Release(ctx context.Context, requestID, leaseID string) (bool, error) {
	resp, err := m.client.ReleaseProviderAccessLeaseExact(ctx, &pluginv1.ReleaseProviderAccessLeaseExactRequest{
		ApiVersion: ProviderAccessV1, RequestId: requestID, LeaseId: leaseID})
	if err != nil {
		return false, err
	}
	return resp.GetRevokedAtProvider(), nil
}

func (spec ProviderAccessLeaseSpec) toProto() *pluginv1.IssueProviderAccessLeaseExactRequest {
	return &pluginv1.IssueProviderAccessLeaseExactRequest{
		ApiVersion: ProviderAccessV1, RequestId: spec.RequestID, IdempotencyKey: spec.IdempotencyKey,
		GrantId: spec.GrantID, WorkspaceId: spec.WorkspaceID, ManagedTaskId: spec.ManagedTaskID,
		SessionId: spec.SessionID, TargetTaskId: spec.TargetTaskID, RepositoryId: spec.RepositoryID,
		Provider: spec.Provider, Purpose: spec.Purpose, Target: spec.Target.toProto(),
	}
}

func (target ProviderAccessGitHubRerunTarget) toProto() *pluginv1.ProviderAccessGitHubRerunTarget {
	return &pluginv1.ProviderAccessGitHubRerunTarget{
		Operation: target.Operation, PrNumber: target.PRNumber,
		BaseRepositoryId: target.BaseRepositoryID, BaseRepository: target.BaseRepository,
		BaseRef: target.BaseRef, BaseSha: target.BaseSHA,
		HeadRepositoryId: target.HeadRepositoryID, HeadRepository: target.HeadRepository,
		HeadRef: target.HeadRef, HeadSha: target.HeadSHA, SourceRunId: target.SourceRunID,
		SourceAttempt: target.SourceAttempt, WorkflowId: target.WorkflowID,
	}
}

func targetFromProto(target *pluginv1.ProviderAccessGitHubRerunTarget) ProviderAccessGitHubRerunTarget {
	if target == nil {
		return ProviderAccessGitHubRerunTarget{}
	}
	return ProviderAccessGitHubRerunTarget{Operation: target.GetOperation(), PRNumber: target.GetPrNumber(),
		BaseRepositoryID: target.GetBaseRepositoryId(), BaseRepository: target.GetBaseRepository(),
		BaseRef: target.GetBaseRef(), BaseSHA: target.GetBaseSha(),
		HeadRepositoryID: target.GetHeadRepositoryId(), HeadRepository: target.GetHeadRepository(),
		HeadRef: target.GetHeadRef(), HeadSHA: target.GetHeadSha(),
		SourceRunID: target.GetSourceRunId(), SourceAttempt: target.GetSourceAttempt(),
		WorkflowID: target.GetWorkflowId()}
}

func (s *grpcHostServer) providerAccessManagerFor() (ProviderAccessManager, error) {
	host, ok := s.impl.(ProviderAccessHost)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "provider access is unavailable on this host")
	}
	manager := host.ProviderAccess()
	if manager == nil {
		return nil, status.Error(codes.Unimplemented, "provider access is unavailable on this host")
	}
	return manager, nil
}

func (s *grpcHostServer) IssueProviderAccessLeaseExact(
	ctx context.Context, req *pluginv1.IssueProviderAccessLeaseExactRequest,
) (*pluginv1.IssueProviderAccessLeaseExactResponse, error) {
	manager, err := s.providerAccessManagerFor()
	if err != nil {
		return nil, err
	}
	if !validProviderAccessIssueRequest(req) {
		return nil, status.Error(codes.InvalidArgument, "invalid provider access contract")
	}
	spec := ProviderAccessLeaseSpec{RequestID: req.GetRequestId(), IdempotencyKey: req.GetIdempotencyKey(),
		GrantID: req.GetGrantId(), WorkspaceID: req.GetWorkspaceId(), ManagedTaskID: req.GetManagedTaskId(),
		SessionID: req.GetSessionId(), TargetTaskID: req.GetTargetTaskId(), RepositoryID: req.GetRepositoryId(),
		Provider: req.GetProvider(), Purpose: req.GetPurpose(), Target: targetFromProto(req.GetTarget())}
	lease, err := manager.Issue(ctx, spec)
	if err != nil {
		return nil, err
	}
	return &pluginv1.IssueProviderAccessLeaseExactResponse{LeaseId: lease.LeaseID,
		ExpiresAt: lease.ExpiresAt.UTC().Format(time.RFC3339Nano), TargetDigest: lease.TargetDigest,
		GrantGeneration: lease.GrantGeneration, ApprovalRevision: lease.ApprovalRevision,
		ConnectionGeneration: lease.ConnectionGeneration, CanonicalRepository: lease.CanonicalRepository,
		Target: lease.Target.toProto()}, nil
}

func validProviderAccessIssueRequest(req *pluginv1.IssueProviderAccessLeaseExactRequest) bool {
	return req.GetApiVersion() == ProviderAccessV1 && req.GetRequestId() != "" &&
		req.GetIdempotencyKey() != "" && req.GetGrantId() != "" &&
		req.GetWorkspaceId() != "" && req.GetManagedTaskId() != "" &&
		req.GetSessionId() != "" && req.GetTargetTaskId() != "" &&
		req.GetRepositoryId() != "" && req.GetProvider() != "" &&
		req.GetPurpose() != "" && req.GetTarget() != nil
}

func (s *grpcHostServer) RedeemProviderAccessLeaseExact(
	ctx context.Context, req *pluginv1.RedeemProviderAccessLeaseExactRequest,
) (*pluginv1.RedeemProviderAccessLeaseExactResponse, error) {
	manager, err := s.providerAccessManagerFor()
	if err != nil {
		return nil, err
	}
	if req.GetApiVersion() != ProviderAccessV1 || req.GetRequestId() == "" || req.GetLeaseId() == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid provider access contract")
	}
	credential, err := manager.Redeem(ctx, req.GetRequestId(), req.GetLeaseId())
	if err != nil {
		return nil, err
	}
	return &pluginv1.RedeemProviderAccessLeaseExactResponse{Token: credential.Bearer(),
		ExpiresAt:           credential.ExpiresAt.UTC().Format(time.RFC3339Nano),
		ProviderPrincipalId: credential.ProviderPrincipalID, CanonicalRepository: credential.CanonicalRepository,
		PermissionProfile: credential.PermissionProfile}, nil
}

func (s *grpcHostServer) ReleaseProviderAccessLeaseExact(
	ctx context.Context, req *pluginv1.ReleaseProviderAccessLeaseExactRequest,
) (*pluginv1.ReleaseProviderAccessLeaseExactResponse, error) {
	manager, err := s.providerAccessManagerFor()
	if err != nil {
		return nil, err
	}
	if req.GetApiVersion() != ProviderAccessV1 || req.GetRequestId() == "" || req.GetLeaseId() == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid provider access contract")
	}
	revoked, err := manager.Release(ctx, req.GetRequestId(), req.GetLeaseId())
	if err != nil {
		return nil, err
	}
	return &pluginv1.ReleaseProviderAccessLeaseExactResponse{RevokedAtProvider: revoked}, nil
}

// UnimplementedHostData keeps the versioned extension unavailable until a
// connection-bound Host explicitly wires an authorized provider access manager.
func (UnimplementedHostData) ProviderAccess() ProviderAccessManager {
	return unimplementedProviderAccessManager{}
}

type unimplementedProviderAccessManager struct{}

func (unimplementedProviderAccessManager) Issue(context.Context, ProviderAccessLeaseSpec) (ProviderAccessLease, error) {
	return ProviderAccessLease{}, status.Error(codes.Unimplemented, "provider access is unavailable on this host")
}

func (unimplementedProviderAccessManager) Redeem(context.Context, string, string) (ProviderAccessCredential, error) {
	return ProviderAccessCredential{}, status.Error(codes.Unimplemented, "provider access is unavailable on this host")
}

func (unimplementedProviderAccessManager) Release(context.Context, string, string) (bool, error) {
	return false, status.Error(codes.Unimplemented, "provider access is unavailable on this host")
}
