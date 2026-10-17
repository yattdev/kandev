package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/auth"
	"github.com/kandev/kandev/internal/auth/httpmw"
	authstore "github.com/kandev/kandev/internal/auth/store"
	"github.com/kandev/kandev/internal/common/config"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/ownanswer"
	"github.com/kandev/kandev/internal/task/service"
	userstore "github.com/kandev/kandev/internal/user/store"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

type approvalHTTPRepo struct {
	authzDeleteRepo
	owner   string
	created int
}

func (r *approvalHTTPRepo) GetTask(context.Context, string) (*models.Task, error) {
	b := ownanswer.ApprovalLinkCase()
	return &models.Task{ID: b.TaskID, WorkspaceID: b.WorkspaceID}, nil
}
func (r *approvalHTTPRepo) GetWorkspace(context.Context, string) (*models.Workspace, error) {
	return &models.Workspace{ID: ownanswer.ApprovalLinkCase().WorkspaceID, OwnerID: r.owner}, nil
}
func (r *approvalHTTPRepo) CreateApprovalLinkGrant(ctx context.Context, req ownanswer.ApprovalLinkRequest) (*ownanswer.ApprovalLinkGrant, error) {
	id, err := ownanswer.BrowserGrantCreator(ctx)
	if err != nil {
		return nil, err
	}
	r.created++
	return &ownanswer.ApprovalLinkGrant{ID: "synthetic-http-grant", CreatorUserID: id.UserID, CreatorSessionID: id.SessionID, ApprovalLinkRequest: req}, nil
}

func approvalHTTPFixture(t *testing.T, disabled ...bool) (*gin.Engine, *auth.Service, string, *approvalHTTPRepo) {
	t.Helper()
	conn, err := sqlx.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	users, cleanup, err := userstore.Provide(conn, conn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cleanup()) })
	store, err := authstore.New(conn, conn)
	require.NoError(t, err)
	cfg := &config.Config{}
	cfg.Features.Auth = true
	cfg.Auth.SessionTTLHours = 1
	authSvc, err := auth.NewService(context.Background(), auth.Deps{Cfg: cfg, Store: store, Users: users})
	require.NoError(t, err)
	user, token, err := authSvc.Setup(context.Background(), "synthetic@example.test", "synthetic-password-123", "Synthetic admin", "", "")
	require.NoError(t, err)
	if len(disabled) > 0 && disabled[0] {
		cfg.Features.Auth = false
		authSvc, err = auth.NewService(context.Background(), auth.Deps{Cfg: cfg, Store: store, Users: users})
		require.NoError(t, err)
	}
	repo := &approvalHTTPRepo{owner: user.ID}
	log := newTestLogger(t)
	svc := service.NewService(service.Repos{Workspaces: repo, Tasks: repo, TaskRepos: repo, Workflows: repo, Messages: repo, Turns: repo, Sessions: repo, GitSnapshots: repo, RepoEntities: repo, Executors: repo, Environments: repo, TaskEnvironments: repo, Reviews: repo}, nil, log, service.RepositoryDiscoveryConfig{})
	h := &TaskHandlers{service: svc, repo: repo, logger: log}
	router := gin.New()
	router.Use(httpmw.Middleware(authSvc))
	h.registerHTTP(router)
	return router, authSvc, token, repo
}

func TestApprovalLinkRealHTTPAuthenticationAndExactCase(t *testing.T) {
	router, authSvc, token, repo := approvalHTTPFixture(t)
	_, pat, err := authSvc.MintToken(context.Background(), repo.owner, "synthetic-approval-denial", 1)
	require.NoError(t, err)
	request := map[string]any{"purpose": ownanswer.ApprovalLinkPurpose, "option_id": "existing-option"}
	raw, err := json.Marshal(ownanswer.ApprovalLinkCase())
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &request))
	for _, tc := range []struct {
		name, credential, mutation string
		want                       int
	}{
		{"anonymous", "", "", http.StatusUnauthorized},
		{"forged-cookie", "forged", "", http.StatusUnauthorized},
		{"pat-admin", pat, "bearer", http.StatusForbidden},
		{"agent-token", "synthetic.agent.jwt", "bearer", http.StatusUnauthorized},
		{"browser", token, "", http.StatusCreated},
		{"forged-author", token, "author_type", http.StatusBadRequest},
		{"foreign-task", token, "task_id", http.StatusForbidden},
		{"foreign-hash", token, "answer_sha256", http.StatusForbidden},
		{"foreign-workspace-owner", token, "owner", http.StatusForbidden},
		{"duplicate-key", token, "duplicate", http.StatusBadRequest},
		{"trailing-document", token, "trailing", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := map[string]any{}
			for k, v := range request {
				payload[k] = v
			}
			if tc.mutation != "" && tc.mutation != "owner" && tc.mutation != "duplicate" && tc.mutation != "trailing" && tc.mutation != "bearer" {
				payload[tc.mutation] = "foreign"
			}
			owner := repo.owner
			if tc.mutation == "owner" {
				repo.owner = "foreign-user"
			}
			defer func() { repo.owner = owner }()
			body, err := json.Marshal(payload)
			require.NoError(t, err)
			if tc.mutation == "duplicate" {
				body = append([]byte(`{"task_id":"foreign",`), body[1:]...)
			}
			if tc.mutation == "trailing" {
				body = append(body, []byte(`{}`)...)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+ownanswer.ApprovalLinkCase().TaskID+"/approval-link-grants", strings.NewReader(string(body)))
			req.Header.Set("Content-Type", "application/json")
			if tc.mutation == "bearer" {
				req.Header.Set("Authorization", "Bearer "+tc.credential)
			} else if tc.credential != "" {
				req.AddCookie(&http.Cookie{Name: authSvc.CookieNameForRequest(req), Value: tc.credential})
			}
			before := repo.created
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			require.Equal(t, tc.want, rec.Code, rec.Body.String())
			if tc.want == http.StatusCreated {
				require.Equal(t, before+1, repo.created)
			} else {
				require.Equal(t, before, repo.created)
			}
		})
	}
}
