package service

import (
	"context"
	"errors"
	"time"

	"github.com/kandev/kandev/internal/auth/authn"
	"github.com/kandev/kandev/internal/authz"
	"github.com/kandev/kandev/internal/task/models"
)

var ErrForceRemovalAdmissionStale = errors.New("force removal admission is stale")
var ErrForceRemovalPreviewInvalid = errors.New("force removal preview is invalid")

type ForceRemovalPreview struct {
	TaskID      string                                   `json:"task_id"`
	WorkspaceID string                                   `json:"workspace_id"`
	Generation  string                                   `json:"generation"`
	Digest      string                                   `json:"digest"`
	Receipts    []models.ExactRetirementPredicateReceipt `json:"receipts"`
}

type ForceRemovalAdmissionRequest struct {
	TaskID, WorkspaceID, ExpectedTaskGeneration, AdmissionGeneration string
	OperationID, RequestDigest, PreviewDigest                        string
}

type forceRemovalAdmissionStore interface {
	ClaimForceRemovalWithReceipts(context.Context, *models.ForceRemovalClaim, []models.ExactRetirementPredicateReceipt) (*models.ForceRemovalClaim, bool, error)
}

// AdmitForceRemoval installs the durable fence and its identity receipt only.
// It never hides a task or invokes cleanup.
func (s *Service) AdmitForceRemoval(ctx context.Context, req ForceRemovalAdmissionRequest) (*models.ForceRemovalClaim, bool, error) {
	if err := s.AuthorizeTaskScope(ctx, req.TaskID, authz.ScopeTaskWrite); err != nil {
		return nil, false, err
	}
	identity, ok := authn.IdentityFromContext(ctx)
	if !ok || !identity.IsAdmin() {
		return nil, false, ErrForbidden
	}
	task, err := s.GetTask(ctx, req.TaskID)
	if err != nil {
		return nil, false, err
	}
	if task.WorkspaceID != req.WorkspaceID || req.ExpectedTaskGeneration != exactRetirementGeneration(task) {
		return nil, false, ErrForceRemovalAdmissionStale
	}
	if req.PreviewDigest != forceRemovalPreviewDigest(task) {
		return nil, false, ErrForceRemovalPreviewInvalid
	}
	store, ok := s.tasks.(forceRemovalAdmissionStore)
	if !ok {
		return nil, false, ErrForceRemovalAdmissionStale
	}
	claim := &models.ForceRemovalClaim{TaskID: task.ID, WorkspaceID: task.WorkspaceID, TaskGeneration: task.UpdatedAt, AdmissionGeneration: req.AdmissionGeneration, OperationID: req.OperationID, RequestDigest: req.RequestDigest, PreviewDigest: req.PreviewDigest}
	receipt := models.ExactRetirementPredicateReceipt{Predicate: models.ExactRetirementIdentityPredicate, Status: models.ExactRetirementReceiptPass, ReasonCode: "EXACT_TASK_CLAIMED", ResourceID: task.ID, ObservedGeneration: task.UpdatedAt.UTC().Format(time.RFC3339Nano), EvidenceDigest: exactRetirementDigest(task.ID, task.WorkspaceID, req.ExpectedTaskGeneration, req.PreviewDigest)}
	return store.ClaimForceRemovalWithReceipts(ctx, claim, []models.ExactRetirementPredicateReceipt{receipt})
}

func forceRemovalPreviewDigest(task *models.Task) string {
	return exactRetirementDigest("force-removal-preview", task.ID, task.WorkspaceID, exactRetirementGeneration(task))
}

// PreviewForceRemoval derives a fresh server-side token and preservation receipts.
func (s *Service) PreviewForceRemoval(ctx context.Context, taskID, workspaceID, expectedGeneration string) (*ForceRemovalPreview, error) {
	if err := s.AuthorizeTaskScope(ctx, taskID, authz.ScopeTaskWrite); err != nil {
		return nil, err
	}
	identity, ok := authn.IdentityFromContext(ctx)
	if !ok || !identity.IsAdmin() {
		return nil, ErrForbidden
	}
	task, err := s.GetTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if task.WorkspaceID != workspaceID || exactRetirementGeneration(task) != expectedGeneration {
		return nil, ErrForceRemovalAdmissionStale
	}
	gen := exactRetirementGeneration(task)
	receipt := models.ExactRetirementPredicateReceipt{Predicate: models.ExactRetirementPreservationPredicate, Status: models.ExactRetirementReceiptUnknown, ReasonCode: "PRESERVED_INVENTORY_UNAVAILABLE", ResourceID: task.ID, ObservedGeneration: gen, EvidenceDigest: exactRetirementDigest("preserved", task.ID, gen)}
	return &ForceRemovalPreview{TaskID: task.ID, WorkspaceID: task.WorkspaceID, Generation: gen, Digest: forceRemovalPreviewDigest(task), Receipts: []models.ExactRetirementPredicateReceipt{receipt}}, nil
}
