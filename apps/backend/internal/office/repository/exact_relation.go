// Package repository defines optional Office storage capabilities.
package repository

import (
	"context"

	"github.com/kandev/kandev/internal/office/models"
)

// ExactRelationSnapshotReader is an opt-in, workspace-bounded blocker-edge
// read capability. It is intentionally separate from the legacy blocker
// interface so existing consumers do not receive exact-read authority.
type ExactRelationSnapshotReader interface {
	OpenExactRelationSnapshot(ctx context.Context, request models.ExactRelationSnapshotRequest) (*models.ExactRelationSnapshot, error)
	PageExactRelationSnapshot(ctx context.Context, token string, offset, limit int) ([]models.ExactTaskRelation, error)
	GetExactRelationSnapshotRelation(ctx context.Context, token, taskID, blockerTaskID string) (*models.ExactTaskRelation, error)
	CleanupExpiredExactRelationSnapshots(ctx context.Context, limit int) (int, error)
}
