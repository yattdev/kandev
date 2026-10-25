// Package repository defines optional Office storage capabilities.
package repository

import (
	"context"

	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/exactsnapshotauthority"
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

// ExactRelationSnapshotTransactionReader is the additive SQLite-only seam for
// a future cross-owner compositor. Its transaction must be issued by the same
// authority's BeginExactRelationSnapshotTx method.
type ExactRelationSnapshotTransactionReader interface {
	BeginExactRelationSnapshotTx(context.Context) (*sqlx.Tx, error)
	OpenExactRelationSnapshotInTx(context.Context, *sqlx.Tx, models.ExactRelationSnapshotRequest) (*models.ExactRelationSnapshot, error)
}

// ExactRelationSnapshotAuthorityReader materializes relation evidence only with
// a provenance-checked transaction from the shared SQLite authority.
type ExactRelationSnapshotAuthorityReader interface {
	ValidateExactRelationSnapshotAuthority(*exactsnapshotauthority.Authority) error
	BeginExactRelationSnapshotAuthorityTx(context.Context, *exactsnapshotauthority.Authority) (*exactsnapshotauthority.Transaction, error)
	OpenExactRelationSnapshotInAuthorityTx(context.Context, *exactsnapshotauthority.Authority, *exactsnapshotauthority.Transaction, models.ExactRelationSnapshotRequest) (*models.ExactRelationSnapshot, error)
}
