package backendapp

import (
	"context"
	"github.com/gin-gonic/gin"
	"github.com/kandev/kandev/internal/auth"
	"github.com/kandev/kandev/internal/auth/authn"
	taskmodels "github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/taskpreview"
	"net/http"
	"path/filepath"
	"time"
)

func registerTaskPreviewRoutes(p routeParams) {
	if p.authSvc == nil || p.taskSvc == nil {
		return
	}
	control := &taskpreview.Control{Store: taskpreview.Store{Directory: "/run/kandev-task-preview"},
		Inspector: taskpreview.DockerInspector{Binary: "/usr/bin/docker", Models: true}, Now: time.Now}
	control.Identity = func(r *http.Request) (taskpreview.Principal, bool) {
		id, ok := authn.IdentityFromContext(r.Context())
		return taskpreview.Principal{UserID: id.UserID, SessionID: id.SessionID, Synthetic: id.Synthetic}, ok
	}
	control.Authorize = func(ctx context.Context, userID string, g taskpreview.Grant) error {
		return authorizeTaskPreview(p, ctx, userID, g)
	}

	for _, path := range []string{"authorize", "credentials"} {
		p.router.GET(taskpreview.ControlPrefix+path, gin.WrapH(control))
	}
	p.router.POST(taskpreview.ControlPrefix+"check", gin.WrapH(control))
}

func authorizeTaskPreview(p routeParams, ctx context.Context, userID string, g taskpreview.Grant) error {
	if p.authSvc.Mode() != auth.ModeEnabled || userID != g.UserID {
		return taskpreview.ErrDenied
	}
	identity, ok := p.authSvc.IdentityForUser(ctx, userID)
	if !ok {
		return taskpreview.ErrDenied
	}
	task, err := p.taskSvc.GetTask(authn.WithIdentity(ctx, identity), g.TaskID)
	if err != nil || task.WorkspaceID != g.WorkspaceID || task.ArchivedAt != nil {
		return taskpreview.ErrDenied
	}
	environment, err := p.taskSvc.GetTaskEnvironmentByTaskID(ctx, g.TaskID)
	if err != nil || !previewEnvironmentMatches(environment, g) {
		return taskpreview.ErrDenied
	}
	return nil
}
func previewEnvironmentMatches(environment *taskmodels.TaskEnvironment, g taskpreview.Grant) bool {
	if environment == nil || environment.ID != g.EnvironmentID || environment.TaskID != g.TaskID {
		return false
	}
	root := environment.WorkspacePath
	if root == "" && environment.TaskDirName != "" {
		root = filepath.Join("/data/tasks", environment.TaskDirName)
	}
	return root == g.TaskRoot
}
