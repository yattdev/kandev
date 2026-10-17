package sqlite

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/auth"
	"github.com/kandev/kandev/internal/auth/authn"
	authstore "github.com/kandev/kandev/internal/auth/store"
	"github.com/kandev/kandev/internal/common/config"
	"github.com/kandev/kandev/internal/task/ownanswer"
	userstore "github.com/kandev/kandev/internal/user/store"
	"github.com/stretchr/testify/require"
)

func seedApprovalBrowserAuthority(t *testing.T, r *Repository) context.Context {
	t.Helper()
	users, cleanup, err := userstore.Provide(r.db, r.db)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cleanup() })
	store, err := authstore.New(r.db, r.db)
	require.NoError(t, err)
	cfg := &config.Config{}
	cfg.Features.Auth = true
	cfg.Auth.SessionTTLHours = 1
	svc, err := auth.NewService(context.Background(), auth.Deps{Cfg: cfg, Store: store, Users: users})
	require.NoError(t, err)
	user, token, err := svc.Setup(context.Background(), "synthetic-approval@example.test", "synthetic-password-123", "Synthetic grant creator", "", "")
	require.NoError(t, err)
	id, ok := svc.ResolveSessionToken(context.Background(), token, "")
	require.True(t, ok)
	_, err = r.db.Exec(`UPDATE workspaces SET owner_id=? WHERE id='answer-workspace'`, user.ID)
	require.NoError(t, err)
	return authn.WithIdentity(context.Background(), id)
}

func TestApprovalLinkConsumerAuthorityContexts(t *testing.T) {
	for _, kind := range []string{"task-owner", "anonymous", "foreign", "member", "synthetic", "pat"} {
		t.Run(kind, func(t *testing.T) {
			r, ctx, b, _, _ := approvalFixture(t)
			g, err := r.createApprovalLinkGrant(ctx, ownanswer.ApprovalLinkRequest{OwnAnswerBinding: b, OptionID: "opt-a"})
			require.NoError(t, err)
			id, _ := authn.IdentityFromContext(ctx)
			id.SessionID = ""
			switch kind {
			case "foreign":
				id.UserID = "foreign"
			case "member":
				id.Role = authn.RoleMember
			case "synthetic":
				id.Synthetic = true
			case "pat":
				id.TokenID = "synthetic-pat"
			}
			ctx = authn.WithIdentity(context.Background(), id)
			if kind == "anonymous" {
				ctx = context.Background()
			}
			before := snapshotOwnAnswerBoundary(t, r)
			_, changed, err := r.consumeApprovalLinkGrant(ctx, b, g.ID, "synthetic-operation", g.Incarnation)
			if kind == "task-owner" {
				require.NoError(t, err)
				require.True(t, changed)
			} else {
				require.Error(t, err)
				require.False(t, changed)
				require.Equal(t, before, snapshotOwnAnswerBoundary(t, r))
			}
		})
	}
}

func TestApprovalLinkRejectsChangedBrowserAuthorityBeforeIssueOrConsume(t *testing.T) {
	for _, phase := range []string{"issue", "consume"} {
		for _, change := range []string{"logout", "expiry", "identity", "role", "disabled", "workspace-owner", "workspace-org"} {
			t.Run(phase+"/"+change, func(t *testing.T) {
				r, ctx, b, _, _ := approvalFixture(t)
				var g *ownanswer.ApprovalLinkGrant
				var err error
				if phase == "consume" {
					g, err = r.createApprovalLinkGrant(ctx, ownanswer.ApprovalLinkRequest{OwnAnswerBinding: b, OptionID: "opt-a"})
					require.NoError(t, err)
				}
				id, _ := authn.IdentityFromContext(ctx)
				switch change {
				case "logout":
					_, err = r.db.Exec(`DELETE FROM auth_sessions WHERE id=?`, id.SessionID)
				case "expiry":
					_, err = r.db.Exec(`UPDATE auth_sessions SET expires_at='2000-01-01 00:00:00' WHERE id=?`, id.SessionID)
				case "identity":
					_, err = r.db.Exec(`UPDATE auth_sessions SET user_id='foreign' WHERE id=?`, id.SessionID)
				case "role":
					_, err = r.db.Exec(`UPDATE users SET role='member' WHERE id=?`, id.UserID)
				case "disabled":
					_, err = r.db.Exec(`UPDATE users SET status='disabled' WHERE id=?`, id.UserID)
				case "workspace-owner":
					_, err = r.db.Exec(`UPDATE workspaces SET owner_id='foreign' WHERE id=?`, b.WorkspaceID)
				case "workspace-org":
					_, err = r.db.Exec(`UPDATE workspaces SET org_id='foreign' WHERE id=?`, b.WorkspaceID)
				}
				require.NoError(t, err)
				before := snapshotOwnAnswerBoundary(t, r)
				if phase == "issue" {
					_, err = r.createApprovalLinkGrant(ctx, ownanswer.ApprovalLinkRequest{OwnAnswerBinding: b, OptionID: "opt-a"})
				} else {
					_, _, err = r.consumeApprovalLinkGrant(ctx, b, g.ID, "synthetic-operation", g.Incarnation)
				}
				require.Error(t, err)
				require.Equal(t, before, snapshotOwnAnswerBoundary(t, r))
			})
		}
	}
}
