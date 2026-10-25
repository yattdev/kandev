package handlers

import (
	"context"

	"github.com/kandev/kandev/internal/coordinator"
	mcpscope "github.com/kandev/kandev/internal/mcp/scope"
)

// coordinatorDependencyAllowed checks both edge endpoints against the same
// workspace and the coordinator's current effective Watches.
func (h *Handlers) coordinatorDependencyAllowed(ctx context.Context, principal mcpscope.Principal, taskID, predecessorID string) bool {
	if taskID == "" || predecessorID == "" || h.taskSvc == nil || h.coordinatorSvc == nil ||
		principal.WorkspaceID == "" || !h.coordinatorWorkspaceMatches(ctx, principal) {
		return false
	}
	dependent, err := h.taskSvc.GetTask(ctx, taskID)
	if err != nil || dependent == nil || dependent.WorkspaceID != principal.WorkspaceID {
		return false
	}
	predecessor, err := h.taskSvc.GetTask(ctx, predecessorID)
	if err != nil || predecessor == nil || predecessor.WorkspaceID != principal.WorkspaceID {
		return false
	}
	refs := []coordinator.TaskRef{
		{ID: dependent.ID, WorkflowID: dependent.WorkflowID},
		{ID: predecessor.ID, WorkflowID: predecessor.WorkflowID},
	}
	allowed, err := h.coordinatorSvc.WatchedTaskIDs(ctx, principal.CoordinatorID, refs)
	return err == nil && allowed[dependent.ID] && allowed[predecessor.ID]
}

func (h *Handlers) coordinatorDependencyPathAllowed(ctx context.Context, principal mcpscope.Principal, ids []string) bool {
	if len(ids) == 0 || h.taskSvc == nil || h.coordinatorSvc == nil ||
		principal.WorkspaceID == "" || !h.coordinatorWorkspaceMatches(ctx, principal) {
		return false
	}
	refs := make([]coordinator.TaskRef, 0, len(ids))
	for _, id := range ids {
		task, err := h.taskSvc.GetTask(ctx, id)
		if err != nil || task == nil || task.WorkspaceID != principal.WorkspaceID {
			return false
		}
		refs = append(refs, coordinator.TaskRef{ID: task.ID, WorkflowID: task.WorkflowID})
	}
	allowed, err := h.coordinatorSvc.WatchedTaskIDs(ctx, principal.CoordinatorID, refs)
	if err != nil {
		return false
	}
	for _, ref := range refs {
		if !allowed[ref.ID] {
			return false
		}
	}
	return true
}
