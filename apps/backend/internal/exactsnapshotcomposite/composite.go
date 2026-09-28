// Package exactsnapshotcomposite combines independently owned exact evidence
// under one sealed SQLite authority transaction.
package exactsnapshotcomposite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/exactsnapshotauthority"
	"github.com/kandev/kandev/internal/office/models"
	officerepository "github.com/kandev/kandev/internal/office/repository"
	mq "github.com/kandev/kandev/internal/orchestrator/messagequeue"
)

const (
	defaultTTL   = 5 * time.Minute
	maxTTL       = 15 * time.Minute
	maxPageSize  = 100
	cleanupLimit = 100
)

var ErrUnavailable = errors.New("exact composite snapshot unavailable")

type relationReader interface {
	officerepository.ExactRelationSnapshotReader
	officerepository.ExactRelationSnapshotAuthorityReader
}

type pendingReader interface {
	mq.ExactPendingTransitionReader
	mq.ExactPendingTransitionAuthorityReader
}

// Repository combines exact relation and deferred-transition evidence. It is
// SQLite-only because Authority cannot issue PostgreSQL transactions.
type Repository struct {
	authority *exactsnapshotauthority.Authority
	relations relationReader
	pending   pendingReader
}

// Request scopes one composite read to a workspace and bounds its lifetime.
type Request struct {
	WorkspaceID string
	TTL         time.Duration
}

// Snapshot identifies one durable, atomic evidence projection.
type Snapshot struct {
	Token       string
	WorkspaceID string
	Version     int64
	ExpiresAt   time.Time
}

// Page holds independently bounded evidence pages from one composite token.
type Page struct {
	Relations          []models.ExactTaskRelation
	PendingTransitions []mq.ExactPendingTransition
}

// PendingTransitionAuthorityOperation runs only after a composite snapshot
// and its observed pending transition are current in the shared authority
// transaction. Repository resolves the transaction after the operation.
type PendingTransitionAuthorityOperation func(context.Context, *exactsnapshotauthority.Authority, *exactsnapshotauthority.Transaction, string) error

// New binds two opt-in readers to a shared authority. Both readers verify the
// writer identity before any composite transaction can begin, so generic or
// mismatched stores fail closed instead of accepting a best-effort composite.
func New(authority *exactsnapshotauthority.Authority, relations relationReader, pending pendingReader) (*Repository, error) {
	if authority == nil || relations == nil || pending == nil {
		return nil, ErrUnavailable
	}
	if err := relations.ValidateExactRelationSnapshotAuthority(authority); err != nil {
		return nil, ErrUnavailable
	}
	if err := pending.ValidateExactPendingTransitionSnapshotAuthority(authority); err != nil {
		return nil, ErrUnavailable
	}
	return &Repository{authority: authority, relations: relations, pending: pending}, nil
}

// Open materializes both source projections and its durable token in one
// authority-issued transaction.
func (r *Repository) Open(ctx context.Context, request Request) (*Snapshot, error) {
	if r == nil || r.authority == nil {
		return nil, ErrUnavailable
	}
	if _, err := r.CleanupExpired(ctx, cleanupLimit); err != nil {
		return nil, ErrUnavailable
	}
	if _, err := r.relations.CleanupExpiredExactRelationSnapshots(ctx, cleanupLimit); err != nil {
		return nil, ErrUnavailable
	}
	if _, err := r.pending.CleanupExpiredExactPendingTransitionSnapshots(ctx, cleanupLimit); err != nil {
		return nil, ErrUnavailable
	}
	tx, err := r.authority.Begin(ctx)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	snapshot, err := r.OpenInAuthorityTx(ctx, tx, request)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return snapshot, nil
}

// OpenInAuthorityTx materializes a composite token without resolving the
// caller-owned authority transaction. It is the rollback-safe composition seam.
func (r *Repository) OpenInAuthorityTx(ctx context.Context, tx *exactsnapshotauthority.Transaction, request Request) (*Snapshot, error) {
	if r == nil || r.authority == nil || !tx.Matches(r.authority) {
		return nil, ErrUnavailable
	}
	ttl, err := validRequest(request)
	if err != nil {
		return nil, err
	}
	if err := initSchema(ctx, tx.SQLX()); err != nil {
		return nil, err
	}
	relationSnapshot, err := r.relations.OpenExactRelationSnapshotInAuthorityTx(ctx, r.authority, tx, models.ExactRelationSnapshotRequest{WorkspaceID: request.WorkspaceID, TTL: ttl})
	if err != nil {
		return nil, ErrUnavailable
	}
	pendingSnapshot, err := r.pending.OpenExactPendingTransitionSnapshotInAuthorityTx(ctx, r.authority, tx, mq.ExactPendingTransitionSnapshotRequest{WorkspaceID: request.WorkspaceID, TTL: ttl})
	if err != nil {
		return nil, ErrUnavailable
	}
	now := time.Now().UTC()
	expiresAt := now.Add(ttl)
	var version int64
	if err := tx.SQLX().GetContext(ctx, &version, `SELECT COALESCE(MAX(version),0)+1 FROM exact_composite_snapshots WHERE workspace_id=?`, request.WorkspaceID); err != nil {
		return nil, err
	}
	snapshot := &Snapshot{Token: uuid.NewString(), WorkspaceID: request.WorkspaceID, Version: version, ExpiresAt: expiresAt}
	_, err = tx.SQLX().ExecContext(ctx, `INSERT INTO exact_composite_snapshots(token,workspace_id,version,relation_snapshot_token,pending_snapshot_token,expires_at) VALUES(?,?,?,?,?,?)`, snapshot.Token, snapshot.WorkspaceID, snapshot.Version, relationSnapshot.Token, pendingSnapshot.Token, snapshot.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return snapshot, nil
}

// Page validates both source fence revisions in one authority transaction
// before reading bounded rows from their durable source projections.
func (r *Repository) Page(ctx context.Context, token string, relationOffset, relationLimit, pendingOffset, pendingLimit int) (*Page, error) {
	if relationOffset < 0 || pendingOffset < 0 || relationLimit < 1 || pendingLimit < 1 || relationLimit > maxPageSize || pendingLimit > maxPageSize {
		return nil, fmt.Errorf("exact composite snapshot page is invalid")
	}
	tx, err := r.beginRead(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	relationToken, pendingToken, err := requireSnapshot(ctx, tx.SQLX(), token)
	if err != nil {
		return nil, err
	}
	page := &Page{}
	if err := tx.SQLX().SelectContext(ctx, &page.Relations, `SELECT task_id,blocker_task_id,workspace_id,task_resource_version,blocker_resource_version,resource_version,created_at FROM exact_relation_snapshot_rows WHERE snapshot_token=? AND ordinal>=? ORDER BY ordinal LIMIT ?`, relationToken, relationOffset, relationLimit); err != nil {
		return nil, err
	}
	if err := tx.SQLX().SelectContext(ctx, &page.PendingTransitions, `SELECT session_id,task_id,workspace_id,session_incarnation_id,workflow_id,workflow_step_id,step_position,queued_at,resource_version,task_resource_version,session_resource_version,queue_generation FROM exact_pending_snapshot_rows WHERE snapshot_token=? AND ordinal>=? ORDER BY ordinal LIMIT ?`, pendingToken, pendingOffset, pendingLimit); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return page, nil
}

// GetRelation returns one relation only when both component projections remain
// current under the composite token.
func (r *Repository) GetRelation(ctx context.Context, token, taskID, blockerTaskID string) (*models.ExactTaskRelation, error) {
	if taskID == "" || blockerTaskID == "" {
		return nil, fmt.Errorf("exact relation endpoint is required")
	}
	tx, err := r.beginRead(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	relationToken, _, err := requireSnapshot(ctx, tx.SQLX(), token)
	if err != nil {
		return nil, err
	}
	var relation models.ExactTaskRelation
	if err := tx.SQLX().GetContext(ctx, &relation, `SELECT task_id,blocker_task_id,workspace_id,task_resource_version,blocker_resource_version,resource_version,created_at FROM exact_relation_snapshot_rows WHERE snapshot_token=? AND task_id=? AND blocker_task_id=?`, relationToken, taskID, blockerTaskID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &relation, nil
}

// GetPendingTransition returns one deferred transition only when both source
// projections remain current under the composite token.
func (r *Repository) GetPendingTransition(ctx context.Context, token, sessionID string) (*mq.ExactPendingTransition, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("exact pending session is required")
	}
	tx, err := r.beginRead(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	_, pendingToken, err := requireSnapshot(ctx, tx.SQLX(), token)
	if err != nil {
		return nil, err
	}
	var pending mq.ExactPendingTransition
	if err := tx.SQLX().GetContext(ctx, &pending, `SELECT session_id,task_id,workspace_id,session_incarnation_id,workflow_id,workflow_step_id,step_position,queued_at,resource_version,task_resource_version,session_resource_version,queue_generation FROM exact_pending_snapshot_rows WHERE snapshot_token=? AND session_id=?`, pendingToken, sessionID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &pending, nil
}

// WithPendingTransitionAuthority validates Host-observed pending evidence and
// runs operation in the same SQLite transaction. It is the hand-off between a
// composite Host read and a command authority; callers receive neither a raw
// database transaction nor a durable grant on validation failure.
func (r *Repository) WithPendingTransitionAuthority(ctx context.Context, token string, observed mq.ExactPendingTransition, operation PendingTransitionAuthorityOperation) error {
	if r == nil || r.authority == nil || operation == nil {
		return ErrUnavailable
	}
	tx, err := r.authority.Begin(ctx)
	if err != nil {
		return ErrUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	_, pendingToken, err := requireSnapshot(ctx, tx.SQLX(), token)
	if err != nil {
		return ErrUnavailable
	}
	if err := r.pending.ValidateExactPendingTransitionInAuthorityTx(ctx, r.authority, tx, pendingToken, observed); err != nil {
		return ErrUnavailable
	}
	if err := operation(ctx, r.authority, tx, pendingToken); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// CleanupExpired deletes a bounded number of expired composite tokens. Source
// snapshots retain their owners' independent cleanup lifecycle.
func (r *Repository) CleanupExpired(ctx context.Context, limit int) (int, error) {
	if r == nil || r.authority == nil || limit < 1 || limit > cleanupLimit {
		return 0, ErrUnavailable
	}
	tx, err := r.authority.Begin(ctx)
	if err != nil {
		return 0, ErrUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	if err := initSchema(ctx, tx.SQLX()); err != nil {
		return 0, err
	}
	result, err := tx.SQLX().ExecContext(ctx, `DELETE FROM exact_composite_snapshots WHERE token IN (SELECT token FROM exact_composite_snapshots WHERE expires_at<=? ORDER BY expires_at,token LIMIT ?)`, time.Now().UTC(), limit)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	return int(n), err
}

func (r *Repository) beginRead(ctx context.Context) (*exactsnapshotauthority.Transaction, error) {
	if r == nil || r.authority == nil {
		return nil, ErrUnavailable
	}
	tx, err := r.authority.Begin(ctx)
	if err != nil {
		return nil, ErrUnavailable
	}
	return tx, nil
}

func validRequest(request Request) (time.Duration, error) {
	if request.WorkspaceID == "" {
		return 0, fmt.Errorf("exact composite workspace is required")
	}
	ttl := request.TTL
	if ttl == 0 {
		ttl = defaultTTL
	}
	if ttl < 0 || ttl > maxTTL {
		return 0, fmt.Errorf("exact composite TTL is invalid")
	}
	return ttl, nil
}

func initSchema(ctx context.Context, tx *sqlx.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS exact_composite_snapshots (token TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, version BIGINT NOT NULL, relation_snapshot_token TEXT NOT NULL, pending_snapshot_token TEXT NOT NULL, expires_at TIMESTAMP NOT NULL); CREATE INDEX IF NOT EXISTS idx_exact_composite_snapshots_expiry ON exact_composite_snapshots(expires_at)`)
	return err
}

func requireSnapshot(ctx context.Context, tx *sqlx.Tx, token string) (string, string, error) {
	if token == "" {
		return "", "", ErrUnavailable
	}
	var workspaceID, relationWorkspaceID, pendingWorkspaceID string
	var relationToken, pendingToken string
	var expiresAt, relationExpires, pendingExpires time.Time
	var relationRevision, relationCurrent, pendingRevision, pendingCurrent int64
	err := tx.QueryRowxContext(ctx, `SELECT c.workspace_id,c.relation_snapshot_token,c.pending_snapshot_token,c.expires_at,rs.workspace_id,rs.expires_at,rs.workspace_revision,rf.revision,ps.workspace_id,ps.expires_at,ps.workspace_revision,pf.revision FROM exact_composite_snapshots c JOIN exact_relation_snapshots rs ON rs.token=c.relation_snapshot_token JOIN exact_relation_workspace_fences rf ON rf.workspace_id=rs.workspace_id JOIN exact_pending_snapshots ps ON ps.token=c.pending_snapshot_token JOIN exact_pending_fences pf ON pf.workspace_id=ps.workspace_id WHERE c.token=?`, token).Scan(&workspaceID, &relationToken, &pendingToken, &expiresAt, &relationWorkspaceID, &relationExpires, &relationRevision, &relationCurrent, &pendingWorkspaceID, &pendingExpires, &pendingRevision, &pendingCurrent)
	if err != nil || workspaceID == "" || workspaceID != relationWorkspaceID || workspaceID != pendingWorkspaceID || time.Now().UTC().Compare(expiresAt) >= 0 || time.Now().UTC().Compare(relationExpires) >= 0 || time.Now().UTC().Compare(pendingExpires) >= 0 || relationRevision != relationCurrent || pendingRevision != pendingCurrent {
		return "", "", ErrUnavailable
	}
	return relationToken, pendingToken, nil
}
