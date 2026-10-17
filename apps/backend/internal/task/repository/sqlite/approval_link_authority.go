package sqlite

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/auth/authn"
	authstore "github.com/kandev/kandev/internal/auth/store"
	"github.com/kandev/kandev/internal/db/dialect"
	"github.com/kandev/kandev/internal/task/ownanswer"
)

func (r *Repository) approvalAuthorityDigest(ctx context.Context, tx *sqlx.Tx, b ownanswer.OwnAnswerBinding, user, session, org string) (string, error) {
	id, ok := authn.IdentityFromContext(ctx)
	if !ok || id.Synthetic || id.Instance || id.TokenID != "" || !id.IsAdmin() || id.UserID != user || id.OrgID != org {
		return "", errApprovalLink
	}
	a, err := authstore.ReadBrowserAuthorityTx(ctx, tx, user, session, r.nowUTC())
	if err != nil || a.Identity.OrgID != org {
		return "", errApprovalLink
	}
	query := `SELECT owner_id,org_id,updated_at FROM workspaces WHERE id=?`
	if dialect.IsPostgres(r.db.DriverName()) {
		query += " FOR SHARE"
	}
	var owner, workspaceOrg string
	var workspaceUpdatedAt time.Time
	err = tx.QueryRowContext(ctx, r.db.Rebind(query), b.WorkspaceID).Scan(&owner, &workspaceOrg, &workspaceUpdatedAt)
	if err != nil || owner != user || workspaceOrg != org {
		return "", errApprovalLink
	}
	raw, err := json.Marshal(struct {
		Browser   *authstore.BrowserAuthority
		Workspace string
		UpdatedAt time.Time
	}{a, b.WorkspaceID, workspaceUpdatedAt})
	return ownanswer.OwnAnswerDigest(raw), err
}

func (r *Repository) checkApprovalAuthority(ctx context.Context, tx *sqlx.Tx, b ownanswer.OwnAnswerBinding, g *ownanswer.ApprovalLinkGrant) error {
	digest, err := r.approvalAuthorityDigest(ctx, tx, b, g.CreatorUserID, g.CreatorSessionID, g.CreatorOrgID)
	if err != nil || digest != g.CreatorAuthoritySHA256 {
		return errApprovalLink
	}
	return nil
}
