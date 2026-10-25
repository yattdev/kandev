package coordinator

import (
	"context"
	"encoding/json"
	"errors"
)

// RecordDependencyDecision durably records one dependency-tool authorization
// decision before the task service can change an edge. Only verified endpoint
// IDs are included in an allowed row; refused calls carry no target data.
func (s *Service) RecordDependencyDecision(
	ctx context.Context, coordinatorID, workspaceID, operation, taskID, predecessorID string, allowed bool,
) error {
	if s == nil || !s.phase2 || coordinatorID == "" || workspaceID == "" ||
		(operation != "add" && operation != "remove") {
		return ErrInvalidActivity
	}
	actualWorkspace, err := s.WorkspaceIDOf(ctx, coordinatorID)
	if err != nil || actualWorkspace != workspaceID {
		return ErrNotFound
	}
	reason := "dependency_" + operation
	row := ActivityRow{
		CoordinatorID: coordinatorID, WorkspaceID: workspaceID,
		ActionClass: ActionUnknown, ReasonCode: &reason,
		Outcome: ActivityRefused, Authorization: AuthDenied,
	}
	if allowed {
		if taskID == "" || predecessorID == "" {
			return ErrInvalidActivity
		}
		detail, marshalErr := json.Marshal(map[string]string{"depends_on_task_id": predecessorID})
		if marshalErr != nil {
			return errors.Join(ErrInvalidActivity, marshalErr)
		}
		row.Outcome = ActivityProposed
		row.Authorization = AuthAutomatic
		row.TargetTaskID = &taskID
		row.Detail = string(detail)
	}
	return s.store.withCoordinatorLock(ctx, coordinatorID, func(tx coordinatorExec) error {
		return s.store.InsertActivity(ctx, tx, row)
	})
}
