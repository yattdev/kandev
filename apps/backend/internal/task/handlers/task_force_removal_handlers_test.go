package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/auth/authn"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/service"
)

func TestHTTPPreviewForceRemovalRequiresExactAuthorizedSnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, repo := newPlanTestHandlersWithRepo(t)
	ctx := context.Background()
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-preview-workspace", Name: "Force", OwnerID: "owner"}))
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "force-preview-task", WorkspaceID: "force-preview-workspace", Title: "Force"}))
	h.service = service.NewService(service.Repos{
		Workspaces: repo, Tasks: repo, TaskRepos: repo, Workflows: repo, Messages: repo,
		Turns: repo, Sessions: repo, GitSnapshots: repo, RepoEntities: repo, Executors: repo,
		Environments: repo, TaskEnvironments: repo, Reviews: repo,
	}, nil, h.logger, service.RepositoryDiscoveryConfig{})
	task, err := h.service.GetTask(ctx, "force-preview-task")
	require.NoError(t, err)

	router := gin.New()
	router.POST("/api/v1/tasks/:id/force-removal/preview", h.httpPreviewForceRemoval)
	body := fmt.Sprintf(`{"workspace_id":%q,"expected_task_generation":%q}`, task.WorkspaceID, task.UpdatedAt.UTC().Format(time.RFC3339Nano))
	request := func(body string, identity authn.Identity) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/force-preview-task/force-removal/preview", strings.NewReader(body))
		req = req.WithContext(authn.WithIdentity(req.Context(), identity))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	viewer := request(body, authn.Identity{UserID: "owner", Role: authn.RoleMember})
	require.Equal(t, http.StatusForbidden, viewer.Code, viewer.Body.String())
	require.NotContains(t, viewer.Body.String(), `"digest"`)

	foreign := request(strings.Replace(body, task.WorkspaceID, "foreign-workspace", 1), authn.Identity{UserID: "owner", Role: authn.RoleAdmin})
	require.Equal(t, http.StatusConflict, foreign.Code, foreign.Body.String())
	require.NotContains(t, foreign.Body.String(), `"digest"`)

	preview := request(body, authn.Identity{UserID: "owner", Role: authn.RoleAdmin})
	require.Equal(t, http.StatusOK, preview.Code, preview.Body.String())
	require.Equal(t, "no-store", preview.Header().Get("Cache-Control"))
	require.Contains(t, preview.Body.String(), `"status":"UNKNOWN"`)
	require.Contains(t, preview.Body.String(), `"digest"`)
}
