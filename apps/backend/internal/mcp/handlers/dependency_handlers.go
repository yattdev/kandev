package handlers

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/agentctl/types/streams"
	mcporigin "github.com/kandev/kandev/internal/mcp/origin"
	"github.com/kandev/kandev/internal/task/dependencywrite"

	ws "github.com/kandev/kandev/pkg/websocket"

	"github.com/kandev/kandev/internal/coordinator"
	mcpscope "github.com/kandev/kandev/internal/mcp/scope"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/service"
)

// dependencyMutationPayload is the wire shape for both dependency tools.
type dependencyMutationPayload struct {
	TaskID          string `json:"task_id"`
	DependsOnTaskID string `json:"depends_on_task_id"`
}

// handleAuditTaskDependencyAttempt records a schema rejection before the
// normal dependency action can reach its authorization guard.
func (h *Handlers) handleAuditTaskDependencyAttempt(ctx context.Context, msg *ws.Message) (*ws.Message, error) {
	if !mcporigin.IsTrustedInternalCall(ctx) {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeUnknownAction, "Unknown action", nil)
	}
	principal, ok := mcpscope.PrincipalFromContext(ctx)
	if !ok || !principal.IsCoordinator() || !h.coordinatorWorkspaceMatches(ctx, principal) {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeNotFound, "target not found", nil)
	}
	var request struct {
		Operation            string `json:"operation"`
		DependentIDPresent   bool   `json:"dependent_id_present"`
		PredecessorIDPresent bool   `json:"predecessor_id_present"`
	}
	if err := json.Unmarshal(msg.Payload, &request); err != nil ||
		(request.Operation != "add" && request.Operation != "remove") {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeBadRequest, "invalid dependency audit payload", nil)
	}
	execution, _ := streams.MCPExecutionContextFromContext(ctx)
	if err := h.coordinatorSvc.RecordDependencyDecision(ctx, principal.CoordinatorID,
		principal.WorkspaceID, request.Operation, "", "", false); err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError, "dependency audit failed", nil)
	}
	if err := h.taskSvc.RecordCoordinatorDependencyDenial(ctx, dependencywrite.DependencyMutationRequest{
		Operation: request.Operation, WorkspaceID: principal.WorkspaceID,
		CallerTaskID: principal.CallerTaskID, CallerSessionID: principal.CallerSessionID,
		CallerExecutionID: execution.ExecutionID, OriginClass: "coordinator", CorrelationID: msg.ID,
		DependentIDPresent: request.DependentIDPresent, PredecessorIDPresent: request.PredecessorIDPresent,
	}, "invalid_payload"); err != nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError, "dependency audit failed", nil)
	}
	return ws.NewResponse(msg.ID, msg.Action, map[string]bool{"audited": true})
}

// handleAddTaskDependency backs add_task_dependency_kandev.
func (h *Handlers) handleAddTaskDependency(ctx context.Context, msg *ws.Message) (*ws.Message, error) {
	req, errMsg := decodeDependencyMutation(msg)
	if errMsg != nil {
		return errMsg, nil
	}
	if denied := h.authorizeDependencyCaller(ctx, msg, req); denied != nil {
		return denied, nil
	}
	if principal, ok := mcpscope.PrincipalFromContext(ctx); ok && principal.IsCoordinator() {
		return h.mutateCoordinatorDependency(ctx, msg, req, "add", principal)
	}
	err := h.taskSvc.AddDependency(ctx, req.TaskID, req.DependsOnTaskID)
	if err != nil {
		var cycle *service.CycleError
		if errors.As(err, &cycle) {
			if principal, ok := mcpscope.PrincipalFromContext(ctx); ok && principal.IsCoordinator() {
				if !h.coordinatorDependencyPathAllowed(ctx, principal, cycle.Path) {
					return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeNotFound, "target not found", nil)
				}
				return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeValidation,
					"would create a dependency cycle", nil)
			}
			return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeValidation, cycle.Error(),
				map[string]interface{}{"cycle": cycle.Path})
		}
		if principal, ok := mcpscope.PrincipalFromContext(ctx); ok && principal.IsCoordinator() {
			return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeValidation, "dependency mutation failed", nil)
		}
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeValidation, err.Error(), nil)
	}
	return h.dependencyResult(ctx, msg, req.TaskID)
}

// handleRemoveTaskDependency backs remove_task_dependency_kandev. Removing an
// absent edge succeeds.
func (h *Handlers) handleRemoveTaskDependency(ctx context.Context, msg *ws.Message) (*ws.Message, error) {
	req, errMsg := decodeDependencyMutation(msg)
	if errMsg != nil {
		return errMsg, nil
	}
	if denied := h.authorizeDependencyCaller(ctx, msg, req); denied != nil {
		return denied, nil
	}
	if principal, ok := mcpscope.PrincipalFromContext(ctx); ok && principal.IsCoordinator() {
		return h.mutateCoordinatorDependency(ctx, msg, req, "remove", principal)
	}
	if err := h.taskSvc.RemoveDependency(ctx, req.TaskID, req.DependsOnTaskID); err != nil {
		if principal, ok := mcpscope.PrincipalFromContext(ctx); ok && principal.IsCoordinator() {
			return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeValidation, "dependency mutation failed", nil)
		}
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeValidation, err.Error(), nil)
	}
	return h.dependencyResult(ctx, msg, req.TaskID)
}

func (h *Handlers) mutateCoordinatorDependency(ctx context.Context, msg *ws.Message,
	req *dependencyMutationPayload, operation string, principal mcpscope.Principal) (*ws.Message, error) {
	execution, ok := streams.MCPExecutionContextFromContext(ctx)
	if !ok || execution.TaskID != principal.CallerTaskID || execution.SessionID != principal.CallerSessionID ||
		execution.ExecutionID == "" || h.coordinatorSvc == nil {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeNotFound, "target not found", nil)
	}
	if req.TaskID == req.DependsOnTaskID {
		if err := h.recordCoordinatorDependencyDenial(ctx, principal, msg,
			"invalid_edge", req.TaskID, req.DependsOnTaskID); err != nil {
			return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError,
				"dependency authorization could not be recorded", nil)
		}
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeValidation,
			"a task cannot depend on itself", nil)
	}
	result, err := h.taskSvc.MutateCoordinatorDependency(ctx, dependencywrite.DependencyMutationRequest{
		Operation: operation, WorkspaceID: principal.WorkspaceID,
		DependentTaskID: req.TaskID, PredecessorTaskID: req.DependsOnTaskID,
		CallerTaskID: principal.CallerTaskID, CallerSessionID: principal.CallerSessionID,
		CallerExecutionID: execution.ExecutionID, OriginClass: "coordinator", CorrelationID: msg.ID,
	}, func(ctx context.Context, tx *sqlx.Tx) error {
		return h.coordinatorSvc.DependencyMutationAllowedTx(ctx, tx, principal.CoordinatorID,
			principal.WorkspaceID, principal.CallerTaskID, principal.CallerSessionID, execution.ExecutionID,
			req.TaskID, req.DependsOnTaskID)
	})
	if err != nil {
		if coordinator.DependencyScopeDenied(err) || errors.Is(err, dependencywrite.ErrCycle) {
			return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeNotFound, "target not found", nil)
		}
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeValidation, "dependency mutation failed", nil)
	}
	dependent, predecessor, err := h.coordinatorDependencySnapshots(ctx, principal, result)
	if err != nil {
		if coordinator.DependencyScopeDenied(err) {
			return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeNotFound, "target not found", nil)
		}
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError,
			"dependency readback could not be authorized", nil)
	}
	return ws.NewResponse(msg.ID, msg.Action, map[string]interface{}{
		keyTaskID: req.TaskID, "depends_on_task_id": req.DependsOnTaskID,
		"changed": result.Changed, "dependent_revision": result.DependentRevision,
		"predecessor_revision": result.PredecessorRevision,
		"dependent":            dependent, "predecessor": predecessor,
	})
}

func (h *Handlers) coordinatorDependencySnapshots(ctx context.Context, principal mcpscope.Principal,
	result dependencywrite.DependencyMutationResult) (map[string]interface{}, map[string]interface{}, error) {
	ids := []string{result.Dependent.TaskID, result.Predecessor.TaskID}
	seen := map[string]bool{result.Dependent.TaskID: true, result.Predecessor.TaskID: true}
	for _, snapshot := range []dependencywrite.DependencySnapshot{result.Dependent, result.Predecessor} {
		for _, group := range [][]dependencywrite.DependencyRef{snapshot.DependsOn, snapshot.Blocks} {
			for _, ref := range group {
				if !seen[ref.ID] {
					ids = append(ids, ref.ID)
					seen[ref.ID] = true
				}
			}
		}
	}
	tasks, err := h.taskSvc.GetTasksByIDs(ctx, ids)
	if err != nil {
		return nil, nil, coordinator.ErrNotFound
	}
	refs := make([]coordinator.TaskRef, 0, len(tasks))
	current := make(map[string]bool, len(tasks))
	for _, task := range tasks {
		if task.WorkspaceID == principal.WorkspaceID {
			refs = append(refs, coordinator.TaskRef{ID: task.ID, WorkflowID: task.WorkflowID})
			current[task.ID] = true
		}
	}
	if !current[result.Dependent.TaskID] || !current[result.Predecessor.TaskID] {
		return nil, nil, coordinator.ErrNotFound
	}
	allowed, err := h.coordinatorSvc.WatchedTaskIDs(ctx, principal.CoordinatorID, refs)
	if err != nil || !allowed[result.Dependent.TaskID] || !allowed[result.Predecessor.TaskID] {
		return nil, nil, coordinator.ErrNotFound
	}
	return dependencySnapshotReadback(result.Dependent, allowed),
		dependencySnapshotReadback(result.Predecessor, allowed), nil
}

func dependencySnapshotReadback(snapshot dependencywrite.DependencySnapshot, allowed map[string]bool) map[string]interface{} {
	dependsOn := make([]dependencywrite.DependencyRef, 0, len(snapshot.DependsOn))
	for _, ref := range snapshot.DependsOn {
		if allowed[ref.ID] {
			dependsOn = append(dependsOn, ref)
		}
	}
	blocks := make([]dependencywrite.DependencyRef, 0, len(snapshot.Blocks))
	for _, ref := range snapshot.Blocks {
		if allowed[ref.ID] {
			blocks = append(blocks, ref)
		}
	}
	blocked, reason := snapshot.Blocked, snapshot.BlockedReason
	if len(dependsOn) != len(snapshot.DependsOn) || snapshot.DependsOnTruncated {
		blocked, reason = true, "unknown"
	}
	return map[string]interface{}{
		"task_id": snapshot.TaskID, "dependency_revision": snapshot.Revision,
		"blocked": blocked, "blocked_reason": reason,
		"depends_on": dependsOn, "blocks": blocks,
		"depends_on_truncated": snapshot.DependsOnTruncated,
		"blocks_truncated":     snapshot.BlocksTruncated,
	}
}

func (h *Handlers) authorizeDependencyCaller(ctx context.Context, msg *ws.Message, req *dependencyMutationPayload) *ws.Message {
	principal, ok := mcpscope.PrincipalFromContext(ctx)
	if !ok || principal.IsAutomation() {
		return nil
	}
	if principal.IsCoordinator() {
		return nil // The transactional writer rechecks the current effective scope.
	} else if req.TaskID == principal.CallerTaskID {
		return nil
	}
	response, _ := ws.NewError(msg.ID, msg.Action, ws.ErrorCodeNotFound, "target not found", nil)
	return response
}

func decodeDependencyMutation(msg *ws.Message) (*dependencyMutationPayload, *ws.Message) {
	var req dependencyMutationPayload
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		errMsg, _ := ws.NewError(msg.ID, msg.Action, ws.ErrorCodeBadRequest, "Invalid payload: "+err.Error(), nil)
		return nil, errMsg
	}
	if req.TaskID == "" || req.DependsOnTaskID == "" {
		errMsg, _ := ws.NewError(msg.ID, msg.Action, ws.ErrorCodeValidation,
			"task_id and depends_on_task_id are required", nil)
		return nil, errMsg
	}
	return &req, nil
}

// dependencyResult returns the task's resulting dependency projection so the
// agent sees the effect of its own call without a follow-up read.
func (h *Handlers) dependencyResult(ctx context.Context, msg *ws.Message, taskID string) (*ws.Message, error) {
	task, err := h.taskSvc.GetTask(ctx, taskID)
	if err != nil || task == nil {
		return ws.NewResponse(msg.ID, msg.Action, map[string]interface{}{keyTaskID: taskID})
	}
	views := h.taskSvc.BuildDependencyViews(ctx, []*models.Task{task})
	view := views[taskID]
	var watched map[string]bool
	principal, hasPrincipal := mcpscope.PrincipalFromContext(ctx)
	coordinatorReadback := hasPrincipal && principal.IsCoordinator()
	if coordinatorReadback {
		if task.WorkspaceID != principal.WorkspaceID {
			return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeNotFound, "target not found", nil)
		}
		refs := make([]coordinator.TaskRef, 0, len(view.DependsOn)+1)
		refs = append(refs, coordinator.TaskRef{ID: task.ID, WorkflowID: task.WorkflowID})
		for _, ref := range view.DependsOn {
			if ref.WorkspaceID == principal.WorkspaceID {
				refs = append(refs, coordinator.TaskRef{ID: ref.ID, WorkflowID: ref.WorkflowID})
			}
		}
		watched, err = h.coordinatorSvc.WatchedTaskIDs(ctx, principal.CoordinatorID, refs)
		if err != nil {
			return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeInternalError,
				"dependency readback could not be authorized", nil)
		}
		if !watched[task.ID] {
			return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeNotFound, "target not found", nil)
		}
	}
	dependsOn := make([]string, 0, len(view.DependsOn))
	for _, ref := range view.DependsOn {
		if coordinatorReadback && !watched[ref.ID] {
			continue
		}
		dependsOn = append(dependsOn, ref.ID)
	}
	return ws.NewResponse(msg.ID, msg.Action, map[string]interface{}{
		keyTaskID:        taskID,
		"blocked":        view.Blocked,
		"blocked_reason": view.BlockedReason,
		"depends_on":     dependsOn,
	})
}
