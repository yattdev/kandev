package provideraccess

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kandev/kandev/internal/auth/authn"
	"github.com/kandev/kandev/internal/authz"
)

type adminGrantAuthorityStub struct {
	allow   bool
	seen    authz.Scope
	misbind bool
}

func (a *adminGrantAuthorityStub) AuthorizeWorkspaceScope(_ context.Context, _ string, scope authz.Scope) error {
	a.seen = scope
	if !a.allow {
		return errors.New("workspace denied")
	}
	return nil
}

func (a *adminGrantAuthorityStub) ResolveGrantScope(_ context.Context, scope GrantScope) (GrantScope, error) {
	if !a.allow {
		return GrantScope{}, ErrGrantUnavailable
	}
	scope.PluginInstallationID = "installation-1"
	if a.misbind {
		scope.WorkspaceID = "workspace-2"
	}
	return scope, nil
}

func grantAdminRouter(t *testing.T, identity *authn.Identity, authority *adminGrantAuthorityStub) (*gin.Engine, *Store) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if identity != nil {
			authn.SetOnGin(c, *identity)
		}
		c.Next()
	})
	router.GET("/api/v1/workspaces/:id", func(c *gin.Context) { c.Status(http.StatusOK) })
	store := newGrantTestStore(t)
	RegisterAdminRoutes(router, store, authority)
	return router, store
}

func TestAdminGrantHTTPRequiresRealWorkspaceAdministrator(t *testing.T) {
	path := "/api/v1/workspaces/workspace-1/provider-access/grants"
	for _, tc := range []struct {
		name     string
		identity *authn.Identity
		allow    bool
		want     int
	}{
		{name: "anonymous", want: http.StatusNotFound},
		{name: "synthetic", identity: &authn.Identity{UserID: "local", Role: authn.RoleAdmin, Synthetic: true}, allow: true, want: http.StatusNotFound},
		{name: "foreign workspace", identity: &authn.Identity{UserID: "other", Role: authn.RoleAdmin}, want: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authority := &adminGrantAuthorityStub{allow: tc.allow}
			router, _ := grantAdminRouter(t, tc.identity, authority)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			if response.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", response.Code, tc.want, response.Body.String())
			}
			if tc.identity != nil && !tc.identity.Synthetic && authority.seen != authz.ScopeSecretManage {
				t.Fatalf("authorized scope = %s, want secret.manage", authority.seen)
			}
		})
	}
}

func TestAdminGrantHTTPCreateListAndRevoke(t *testing.T) {
	identity := &authn.Identity{UserID: "owner-1", Role: authn.RoleMember}
	authority := &adminGrantAuthorityStub{allow: true}
	router, store := grantAdminRouter(t, identity, authority)
	path := "/api/v1/workspaces/workspace-1/provider-access/grants"
	body := fmt.Sprintf(`{"plugin_id":"coordinator","conversation_key":"conversation-1",`+
		`"target_task_id":"target-1","repository_id":"repo-row-1","provider":"github",`+
		`"purpose":"actions_write","expires_at":%q}`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body)))
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", response.Code, response.Body.String())
	}
	var created struct {
		ID                   string `json:"id"`
		PluginInstallationID string `json:"plugin_installation_id"`
		CreatedByUserID      string `json:"created_by_user_id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.PluginInstallationID != "installation-1" || created.CreatedByUserID != "owner-1" {
		t.Fatalf("created grant = %+v", created)
	}
	if stored, err := store.GetGrant(context.Background(), created.ID); err != nil || stored == nil ||
		stored.WorkspaceID != "workspace-1" {
		t.Fatalf("stored grant = %+v, err = %v", stored, err)
	}
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(created.ID)) {
		t.Fatalf("list status = %d: %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, path+"/"+created.ID, nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d: %s", response.Code, response.Body.String())
	}
	if stored, err := store.GetGrant(context.Background(), created.ID); err != nil || stored == nil || stored.RevokedAt == nil {
		t.Fatalf("revoked grant = %+v, err = %v", stored, err)
	}
}

func TestAdminGrantHTTPRejectsUnknownRequestFields(t *testing.T) {
	router, _ := grantAdminRouter(t, &authn.Identity{UserID: "owner-1", Role: authn.RoleMember},
		&adminGrantAuthorityStub{allow: true})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost,
		"/api/v1/workspaces/workspace-1/provider-access/grants", bytes.NewBufferString(`{"token":"caller-secret"}`)))
	if response.Code != http.StatusBadRequest || bytes.Contains(response.Body.Bytes(), []byte("caller-secret")) {
		t.Fatalf("unknown field response = %d: %s", response.Code, response.Body.String())
	}
}

func TestAdminGrantHTTPRejectsMisboundAuthorityResult(t *testing.T) {
	authority := &adminGrantAuthorityStub{allow: true, misbind: true}
	router, store := grantAdminRouter(t, &authn.Identity{UserID: "owner-1", Role: authn.RoleMember}, authority)
	body := fmt.Sprintf(`{"plugin_id":"coordinator","conversation_key":"conversation-1",`+
		`"target_task_id":"target-1","repository_id":"repo-row-1","provider":"github",`+
		`"purpose":"actions_write","expires_at":%q}`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost,
		"/api/v1/workspaces/workspace-1/provider-access/grants", bytes.NewBufferString(body)))
	if response.Code != http.StatusForbidden {
		t.Fatalf("misbound result status = %d: %s", response.Code, response.Body.String())
	}
	grants, err := store.ListWorkspaceGrants(context.Background(), "workspace-2")
	if err != nil || len(grants) != 0 {
		t.Fatalf("cross-workspace grants = %+v, err = %v", grants, err)
	}
}

func TestAdminGrantHTTPRejectsOutOfWindowExpiry(t *testing.T) {
	for _, expiry := range []time.Time{time.Now().Add(-time.Minute), time.Now().Add(25 * time.Hour)} {
		router, store := grantAdminRouter(t, &authn.Identity{UserID: "owner-1", Role: authn.RoleMember},
			&adminGrantAuthorityStub{allow: true})
		body := fmt.Sprintf(`{"plugin_id":"coordinator","conversation_key":"conversation-1",`+
			`"target_task_id":"target-1","repository_id":"repo-row-1","provider":"github",`+
			`"purpose":"actions_write","expires_at":%q}`, expiry.UTC().Format(time.RFC3339))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPost,
			"/api/v1/workspaces/workspace-1/provider-access/grants", bytes.NewBufferString(body)))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("expiry %s status = %d: %s", expiry, response.Code, response.Body.String())
		}
		grants, err := store.ListWorkspaceGrants(context.Background(), "workspace-1")
		if err != nil || len(grants) != 0 {
			t.Fatalf("invalid expiry grants = %+v, err = %v", grants, err)
		}
	}
}
