package handlers

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/kandev/kandev/internal/common/securityutil"
	"github.com/kandev/kandev/internal/mcp/profile"
	"github.com/kandev/kandev/internal/mcp/scope"
	"github.com/kandev/kandev/internal/task/models"
)

func validateScopedPRURLFields(fields map[string]json.RawMessage, p taskChangeRequestPayload) error {
	_, hasURL := fields["pr_url"]
	_, hasHead := fields["expected_head_sha"]
	if !hasURL && !hasHead {
		return nil
	}
	if !hasURL || !hasHead || p.PRURL == "" || len(p.ExpectedHeadSHA) != 40 || !securityutil.LooksLikeCommitSHA(p.ExpectedHeadSHA) || p.Operation != "link" || p.Provider != "github" {
		return errors.New("pr_url and a full expected_head_sha require a GitHub link operation")
	}
	return nil
}
func (h *Handlers) authorizeScopedPRURL(ctx context.Context, p scope.Principal, target *models.Task) error {
	if p.Surface != profile.SurfaceKanbanTask && p.Surface != profile.SurfaceOfficeTask {
		return errors.New("task-bound parent session required")
	}
	caller, err := h.taskSvc.GetTask(ctx, p.CallerTaskID)
	if err != nil || caller == nil || caller.ArchivedAt != nil || target.ArchivedAt != nil || !canDirectParentAccess(caller, target) {
		return errors.New("active direct parent required")
	}
	return h.authorizeScopedPRSession(ctx, p, caller)
}

func (h *Handlers) authorizeScopedPRSession(ctx context.Context, p scope.Principal, caller *models.Task) error {
	session, err := h.taskSvc.GetTaskSession(ctx, p.CallerSessionID)
	if err != nil || session == nil || session.TaskID != caller.ID || session.State != models.TaskSessionStateRunning {
		return errors.New("running parent session required")
	}
	if config, _ := session.Metadata["config_mode"].(bool); config {
		return errors.New("configuration session cannot associate PRs")
	}
	if caller.Origin == models.TaskOriginAutomationRun || caller.Origin == models.TaskOriginCoordinator {
		return errors.New("task-bound parent session required")
	}
	_, managed, policyErr := models.ManagedToolPolicyFromTask(caller)
	if managed || policyErr != nil {
		return errors.New("managed conversation cannot associate PRs")
	}
	return nil
}
