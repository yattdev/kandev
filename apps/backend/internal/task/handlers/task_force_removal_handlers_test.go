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

func TestHTTPCommitForceRemovalUsesAuthenticatedHumanAdmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, repo := newPlanTestHandlersWithRepo(t)
	ctx := context.Background()
	require.NoError(t, repo.CreateWorkspace(ctx, &models.Workspace{ID: "force-commit-workspace", Name: "Force", OwnerID: "owner"}))
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "force-commit-task", WorkspaceID: "force-commit-workspace", Title: "Force"}))
	h.service = service.NewService(service.Repos{
		Workspaces: repo, Tasks: repo, TaskRepos: repo, Workflows: repo, Messages: repo,
		Turns: repo, Sessions: repo, GitSnapshots: repo, RepoEntities: repo, Executors: repo,
		Environments: repo, TaskEnvironments: repo, Reviews: repo,
	}, nil, h.logger, service.RepositoryDiscoveryConfig{})
	task, err := h.service.GetTask(ctx, "force-commit-task")
	require.NoError(t, err)
	admin := authn.Identity{UserID: "owner", Role: authn.RoleAdmin}
	preview, err := h.service.PreviewForceRemoval(authn.WithIdentity(ctx, admin), task.ID, task.WorkspaceID, task.UpdatedAt.UTC().Format(time.RFC3339Nano))
	require.NoError(t, err)

	router := gin.New()
	router.POST("/api/v1/tasks/:id/force-removal/commit", h.httpCommitForceRemoval)
	body := fmt.Sprintf(`{"confirmation_task_id":%q,"workspace_id":%q,"expected_task_generation":%q,"admission_generation":"admission","operation_id":"http-op","request_digest":"request","preview_digest":%q}`, task.ID, task.WorkspaceID, task.UpdatedAt.UTC().Format(time.RFC3339Nano), preview.Digest)
	request := func(identity authn.Identity, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/force-commit-task/force-removal/commit", strings.NewReader(body))
		req = req.WithContext(authn.WithIdentity(req.Context(), identity))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	viewer := request(authn.Identity{UserID: "owner", Role: authn.RoleMember}, body)
	require.Equal(t, http.StatusForbidden, viewer.Code, viewer.Body.String())
	require.NotContains(t, viewer.Body.String(), "operation_id")

	wrongConfirmation := request(admin, strings.Replace(body, task.ID, "other-task", 1))
	require.Equal(t, http.StatusBadRequest, wrongConfirmation.Code, wrongConfirmation.Body.String())
	require.NotContains(t, wrongConfirmation.Body.String(), "operation_id")

	stale := request(admin, strings.Replace(body, preview.Digest, "stale", 1))
	require.Equal(t, http.StatusConflict, stale.Code, stale.Body.String())
	require.NotContains(t, stale.Body.String(), "operation_id")

	committed := request(admin, body)
	require.Equal(t, http.StatusOK, committed.Code, committed.Body.String())
	require.Equal(t, "no-store", committed.Header().Get("Cache-Control"))
	require.Contains(t, committed.Body.String(), `"task_id":"force-commit-task"`)
	require.NotContains(t, committed.Body.String(), "caller_task_id")
	require.NotContains(t, committed.Body.String(), "caller_session_id")
}
