package sqlite

import (
	"context"

	"github.com/kandev/kandev/internal/task/ownanswer"
)

func (r *Repository) RevokeApprovalLinkGrant(ctx context.Context, id string) error {
	return r.revokeApprovalLinkGrant(ctx, ownanswer.ApprovalLinkCase(), id)
}

func (r *Repository) revokeApprovalLinkGrant(ctx context.Context, b ownanswer.OwnAnswerBinding, id string) error {
	creator, err := ownanswer.BrowserGrantCreator(ctx)
	if err != nil {
		return err
	}
	if id == "" || len(id) > 128 {
		return errApprovalLink
	}
	tx, release, err := r.beginPlanCommentTx(ctx, b.TaskID)
	if err != nil {
		return err
	}
	defer release()
	defer func() { _ = tx.Rollback() }()
	if err = r.guardOwnAnswerScope(ctx, tx, b); err != nil {
		return err
	}
	g, state, _, err := r.readApprovalLinkGrant(ctx, tx, b, id)
	if err != nil {
		return err
	}
	if !approvalGrantCreatorMatches(g, b, creator.UserID, creator.SessionID) {
		return errApprovalLink
	}
	if err = r.checkApprovalAuthority(ctx, tx, b, g); err != nil {
		return err
	}
	if state == "revoked" {
		return tx.Commit()
	}
	if state != "issued" {
		return errApprovalLink
	}
	result, err := tx.ExecContext(ctx, r.db.Rebind(`UPDATE own_answer_approval_link_grants SET state='revoked' WHERE id=? AND state='issued' AND operation_id=''`), id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return errApprovalLink
	}
	return tx.Commit()
}

func approvalGrantCreatorMatches(g *ownanswer.ApprovalLinkGrant, b ownanswer.OwnAnswerBinding, user, session string) bool {
	return g.CreatorUserID == user && g.CreatorSessionID == session && g.OwnAnswerBinding == b && g.Purpose == ownanswer.ApprovalLinkPurpose
}
