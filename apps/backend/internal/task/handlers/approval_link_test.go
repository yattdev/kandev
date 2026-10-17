package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kandev/kandev/internal/auth/authn"
	"github.com/stretchr/testify/require"
)

func TestApprovalLinkCreatorIngressDeniesUntrustedIdentity(t *testing.T) {
	for name, id := range map[string]*authn.Identity{
		"anonymous":       nil,
		"synthetic-admin": {UserID: "operator", Role: authn.RoleAdmin, Synthetic: true},
		"pat-admin":       {UserID: "operator", Role: authn.RoleAdmin, TokenID: "pat"},
		"task-owner":      {UserID: "operator", Role: authn.RoleAdmin},
		"member":          {UserID: "operator", Role: authn.RoleMember, SessionID: "browser"},
	} {
		t.Run(name, func(t *testing.T) {
			router := gin.New()
			router.Use(func(c *gin.Context) {
				if id != nil {
					authn.SetOnGin(c, *id)
				}
			})
			h := &TaskHandlers{}
			h.registerHTTP(router)
			for _, method := range []string{http.MethodPost, http.MethodDelete} {
				path := "/api/v1/tasks/target/approval-link-grants"
				if method == http.MethodDelete {
					path += "/synthetic-grant"
				}
				request := httptest.NewRequest(method, path, strings.NewReader(`{"author_type":"user","user_id":"operator"}`))
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				require.Equal(t, http.StatusForbidden, response.Code)
			}
		})
	}
}
