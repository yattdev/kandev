package provideraccess

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kandev/kandev/internal/auth/authn"
	"github.com/kandev/kandev/internal/authz"
)

const maxGrantRequestBytes = 4096
const maxGrantLifetime = 24 * time.Hour

// AdminGrantAuthority resolves the administrator's workspace scope and the
// current installation, approval, target and provider principal.
type AdminGrantAuthority interface {
	AuthorizeWorkspaceScope(context.Context, string, authz.Scope) error
	ResolveGrantScope(context.Context, GrantScope) (GrantScope, error)
}

type adminGrantController struct {
	store     *Store
	authority AdminGrantAuthority
}

// RegisterAdminRoutes exposes only non-secret grant administration. Credential
// issuance and redemption use a separate connection-bound Host contract.
func RegisterAdminRoutes(router *gin.Engine, store *Store, authority AdminGrantAuthority) {
	if router == nil || store == nil || authority == nil {
		return
	}
	c := &adminGrantController{store: store, authority: authority}
	group := router.Group("/api/v1/workspaces/:id/provider-access/grants", authn.RequireRealIdentity())
	group.POST("", c.create)
	group.GET("", c.list)
	group.DELETE("/:grant_id", c.revoke)
}

type createGrantRequest struct {
	PluginID        string    `json:"plugin_id"`
	ConversationKey string    `json:"conversation_key"`
	TargetTaskID    string    `json:"target_task_id"`
	RepositoryID    string    `json:"repository_id"`
	Provider        string    `json:"provider"`
	Purpose         string    `json:"purpose"`
	ExpiresAt       time.Time `json:"expires_at"`
}

type grantDTO struct {
	ID                   string     `json:"id"`
	PluginInstallationID string     `json:"plugin_installation_id"`
	PluginID             string     `json:"plugin_id"`
	WorkspaceID          string     `json:"workspace_id"`
	ConversationKey      string     `json:"conversation_key"`
	TargetTaskID         string     `json:"target_task_id"`
	RepositoryID         string     `json:"repository_id"`
	Provider             string     `json:"provider"`
	Purpose              string     `json:"purpose"`
	Generation           int64      `json:"generation"`
	CreatedByUserID      string     `json:"created_by_user_id"`
	ExpiresAt            time.Time  `json:"expires_at"`
	RevokedAt            *time.Time `json:"revoked_at,omitempty"`
}

func grantResponse(grant Grant) grantDTO {
	return grantDTO{ID: grant.ID, PluginInstallationID: grant.PluginInstallationID,
		PluginID: grant.PluginID, WorkspaceID: grant.WorkspaceID,
		ConversationKey: grant.ConversationKey, TargetTaskID: grant.TargetTaskID,
		RepositoryID: grant.RepositoryID, Provider: grant.Provider, Purpose: grant.Purpose,
		Generation: grant.Generation, CreatedByUserID: grant.CreatedByUserID,
		ExpiresAt: grant.ExpiresAt, RevokedAt: grant.RevokedAt}
}

func (c *adminGrantController) authorize(ctx *gin.Context) bool {
	if err := c.authority.AuthorizeWorkspaceScope(ctx.Request.Context(),
		ctx.Param("id"), authz.ScopeSecretManage); err != nil {
		ctx.JSON(http.StatusForbidden, gin.H{"error": "workspace grant access denied"})
		return false
	}
	return true
}

func (c *adminGrantController) create(ctx *gin.Context) {
	if !c.authorize(ctx) {
		return
	}
	identity, ok := authn.FromGin(ctx)
	if !ok || identity.UserID == "" {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, maxGrantRequestBytes)
	decoder := json.NewDecoder(ctx.Request.Body)
	decoder.DisallowUnknownFields()
	var input createGrantRequest
	if err := decoder.Decode(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid grant request"})
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid grant request"})
		return
	}
	now := time.Now().UTC()
	if !input.ExpiresAt.After(now) || input.ExpiresAt.After(now.Add(maxGrantLifetime)) {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid grant expiry"})
		return
	}
	requested := GrantScope{
		PluginID: input.PluginID, WorkspaceID: ctx.Param("id"),
		ConversationKey: input.ConversationKey, TargetTaskID: input.TargetTaskID,
		RepositoryID: input.RepositoryID, Provider: input.Provider, Purpose: input.Purpose,
	}
	scope, err := c.authority.ResolveGrantScope(ctx.Request.Context(), requested)
	requested.PluginInstallationID = scope.PluginInstallationID
	if err != nil || scope.PluginInstallationID == "" || scope != requested {
		ctx.JSON(http.StatusForbidden, gin.H{"error": "provider grant scope unavailable"})
		return
	}
	grant := Grant{ID: uuid.NewString(), GrantScope: scope,
		CreatedByUserID: identity.UserID, ExpiresAt: input.ExpiresAt}
	if err := c.store.ReplaceGrant(ctx.Request.Context(), &grant); err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": "provider grant unavailable"})
		return
	}
	ctx.JSON(http.StatusCreated, grantResponse(grant))
}

func (c *adminGrantController) list(ctx *gin.Context) {
	if !c.authorize(ctx) {
		return
	}
	grants, err := c.store.ListWorkspaceGrants(ctx.Request.Context(), ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "provider grants unavailable"})
		return
	}
	out := make([]grantDTO, 0, len(grants))
	for _, grant := range grants {
		out = append(out, grantResponse(grant))
	}
	ctx.JSON(http.StatusOK, gin.H{"grants": out})
}

func (c *adminGrantController) revoke(ctx *gin.Context) {
	if !c.authorize(ctx) {
		return
	}
	grant, err := c.store.GetGrant(ctx.Request.Context(), ctx.Param("grant_id"))
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "provider grant unavailable"})
		return
	}
	if grant == nil || grant.WorkspaceID != ctx.Param("id") {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "provider grant not found"})
		return
	}
	if err := c.store.RevokeGrant(ctx.Request.Context(), grant.WorkspaceID, grant.ID, time.Now().UTC()); err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": "provider grant unavailable"})
		return
	}
	ctx.Status(http.StatusNoContent)
}
