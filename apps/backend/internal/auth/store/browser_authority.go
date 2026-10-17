package store

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/auth/authn"
	"github.com/kandev/kandev/internal/db/dialect"
)

// BrowserAuthority contains no credential material. Its revision fields fence
// account changes and deletion/recreation of the browser session identity.
type BrowserAuthority struct {
	Identity         authn.Identity
	UserUpdatedAt    time.Time
	SessionCreatedAt time.Time
}

// ReadBrowserAuthorityTx validates a real admin session within the caller's
// transaction. Shared PostgreSQL locks keep revocation writers behind commit.
func ReadBrowserAuthorityTx(ctx context.Context, tx *sqlx.Tx, user, session string, at time.Time) (*BrowserAuthority, error) {
	query := `SELECT u.id,u.role,u.org_id,u.is_operator,u.status,u.updated_at,s.created_at,s.expires_at FROM auth_sessions s JOIN users u ON u.id=s.user_id WHERE s.id=? AND s.user_id=?`
	if dialect.IsPostgres(tx.DriverName()) {
		query += " FOR SHARE OF s,u"
	}
	a := &BrowserAuthority{}
	var status string
	var expiry time.Time
	err := tx.QueryRowContext(ctx, tx.Rebind(query), session, user).Scan(&a.Identity.UserID, &a.Identity.Role, &a.Identity.OrgID, &a.Identity.Instance, &status, &a.UserUpdatedAt, &a.SessionCreatedAt, &expiry)
	if err != nil {
		return nil, err
	}
	if a.Identity.Role != authn.RoleAdmin || a.Identity.Instance || status != "active" || !at.Before(expiry) {
		return nil, ErrNotFound
	}
	a.Identity.SessionID = session
	return a, nil
}
