package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mcpscope "github.com/kandev/kandev/internal/mcp/scope"
	"github.com/kandev/kandev/internal/task/ownanswer"
	"github.com/stretchr/testify/require"
)

// Requested contract coverage exercises current server authentication after a
// browser authority change, rather than injecting a trusted identity struct.
func TestApprovalLinkHTTPRejectsChangedBrowserAuthority(t *testing.T) {
	for _, change := range []string{"logout", "role", "disabled", "foreign-identity"} {
		t.Run(change, func(t *testing.T) {
			router, svc, token, repo := approvalHTTPFixture(t)
			id, ok := svc.ResolveSessionToken(context.Background(), token, "")
			require.True(t, ok)
			_, err := svc.AdminCreateUser(context.Background(), "other@example.test", "synthetic-password-123", "Other admin", "admin")
			require.NoError(t, err)
			want := http.StatusForbidden
			switch change {
			case "logout":
				require.NoError(t, svc.Logout(context.Background(), id.SessionID, id.UserID))
				want = http.StatusUnauthorized
			case "role":
				_, err = svc.AdminSetRoleStatus(context.Background(), id.UserID, "member", "")
			case "disabled":
				_, err = svc.AdminSetRoleStatus(context.Background(), id.UserID, "", "disabled")
				want = http.StatusUnauthorized
			case "foreign-identity":
				_, token, err = svc.Login(context.Background(), "other@example.test", "synthetic-password-123", "", "")
			}
			require.NoError(t, err)
			body, err := json.Marshal(approvalLinkCreation{ApprovalLinkRequest: ownanswer.ApprovalLinkRequest{OwnAnswerBinding: ownanswer.ApprovalLinkCase(), OptionID: "existing-option"}, Purpose: ownanswer.ApprovalLinkPurpose})
			require.NoError(t, err)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+ownanswer.ApprovalLinkCase().TaskID+"/approval-link-grants", strings.NewReader(string(body)))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(&http.Cookie{Name: svc.CookieNameForRequest(req), Value: token})
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			require.Equal(t, want, rec.Code, rec.Body.String())
			require.Zero(t, repo.created)
		})
	}
}

func TestApprovalLinkHTTPDeniesMCPPrincipalAlongsideBrowserCookie(t *testing.T) {
	router, svc, token, repo := approvalHTTPFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/target/approval-link-grants", strings.NewReader(`{}`))
	req.AddCookie(&http.Cookie{Name: svc.CookieNameForRequest(req), Value: token})
	req = req.WithContext(mcpscope.WithPrincipal(req.Context(), mcpscope.Principal{CallerTaskID: "task", CallerSessionID: "session"}))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Zero(t, repo.created)
}

func TestApprovalLinkHTTPDeniesAuthDisabledSyntheticAdministrator(t *testing.T) {
	router, _, _, repo := approvalHTTPFixture(t, true)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/target/approval-link-grants", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Zero(t, repo.created)
}
