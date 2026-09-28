package plugins

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

const exactTaskGrantTTL = 5 * time.Minute

var ErrExactTaskGrantUnavailable = errors.New("plugins: exact task grant is unavailable")

// ExactTaskGrant is an opaque Host-issued authority record. It is deliberately
// not a task command and cannot mutate task state on its own.
type ExactTaskGrant struct {
	ID, InstallationID, WorkspaceID, TaskID string
	CapabilityID, ApprovalAuditID           string
	ApprovalRevision                        uint64
	ActionDigest, IdempotencyKey            string
	IssuedAt, ExpiresAt                     time.Time
	RevokedAt                               *time.Time `json:"revoked_at,omitempty"`
}

type ExactTaskGrantRequest struct {
	InstallationID, WorkspaceID, TaskID string
	CapabilityID, ApprovalAuditID       string
	ApprovalRevision                    uint64
	ActionDigest, IdempotencyKey        string
}

func (l *approvalLedger) issueExactTaskGrant(request ExactTaskGrantRequest, now time.Time) (ExactTaskGrant, error) {
	if l == nil || !validExactTaskGrantRequest(request) {
		return ExactTaskGrant{}, ErrExactTaskGrantUnavailable
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	file, err := l.load()
	if err != nil {
		return ExactTaskGrant{}, err
	}
	if !hasExactTaskGrantReceipt(file.ReadReceipts, request) {
		return ExactTaskGrant{}, ErrExactTaskGrantUnavailable
	}
	for _, grant := range file.ExactTaskGrants {
		if grant.InstallationID == request.InstallationID && grant.WorkspaceID == request.WorkspaceID && grant.IdempotencyKey == request.IdempotencyKey {
			if grant.TaskID == request.TaskID && grant.ActionDigest == request.ActionDigest && grant.ApprovalAuditID == request.ApprovalAuditID && grant.ApprovalRevision == request.ApprovalRevision {
				return grant, nil
			}
			return ExactTaskGrant{}, ErrExactTaskGrantUnavailable
		}
	}
	grant := ExactTaskGrant{ID: uuid.NewString(), InstallationID: request.InstallationID, WorkspaceID: request.WorkspaceID, TaskID: request.TaskID, CapabilityID: request.CapabilityID, ApprovalAuditID: request.ApprovalAuditID, ApprovalRevision: request.ApprovalRevision, ActionDigest: request.ActionDigest, IdempotencyKey: request.IdempotencyKey, IssuedAt: now.UTC(), ExpiresAt: now.UTC().Add(exactTaskGrantTTL)}
	file.ExactTaskGrants[grant.ID] = grant
	return grant, l.save(file)
}

func hasExactTaskGrantReceipt(receipts []ApprovalReceipt, request ExactTaskGrantRequest) bool {
	for _, receipt := range receipts {
		if receipt.Result == approvalReceiptAllowed && receipt.AuditID == request.ApprovalAuditID && receipt.InstallationID == request.InstallationID && receipt.WorkspaceID == request.WorkspaceID && receipt.CapabilityID == request.CapabilityID && receipt.Revision == request.ApprovalRevision {
			return true
		}
	}
	return false
}

func (l *approvalLedger) requireExactTaskGrant(id string, request ExactTaskGrantRequest, now time.Time) (ExactTaskGrant, error) {
	if l == nil || id == "" || !validExactTaskGrantRequest(request) {
		return ExactTaskGrant{}, ErrExactTaskGrantUnavailable
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	file, err := l.load()
	if err != nil {
		return ExactTaskGrant{}, err
	}
	grant, ok := file.ExactTaskGrants[id]
	if !ok || !grantMatchesRequest(grant, request, now) {
		return ExactTaskGrant{}, ErrExactTaskGrantUnavailable
	}
	return grant, nil
}

func grantMatchesRequest(grant ExactTaskGrant, request ExactTaskGrantRequest, now time.Time) bool {
	return grant.RevokedAt == nil && grant.ExpiresAt.After(now.UTC()) && grant.InstallationID == request.InstallationID && grant.WorkspaceID == request.WorkspaceID && grant.TaskID == request.TaskID && grant.CapabilityID == request.CapabilityID && grant.ApprovalAuditID == request.ApprovalAuditID && grant.ApprovalRevision == request.ApprovalRevision && grant.ActionDigest == request.ActionDigest && grant.IdempotencyKey == request.IdempotencyKey
}

func validExactTaskGrantRequest(r ExactTaskGrantRequest) bool {
	return isBoundedApprovalIdentifier(r.InstallationID) && isBoundedApprovalIdentifier(r.WorkspaceID) && isBoundedApprovalIdentifier(r.TaskID) && isExactHostV2Capability(r.CapabilityID) && isBoundedApprovalIdentifier(r.ApprovalAuditID) && r.ApprovalRevision > 0 && isBoundedAuthorizationData(r.ActionDigest) && isBoundedAuthorizationData(r.IdempotencyKey)
}
