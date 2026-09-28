package handlers

import (
	"context"
	"errors"
	"testing"

	mcpprofile "github.com/kandev/kandev/internal/mcp/profile"
	mcpscope "github.com/kandev/kandev/internal/mcp/scope"
	"github.com/kandev/kandev/internal/orchestrator"
	"github.com/kandev/kandev/internal/task/models"
	taskrepo "github.com/kandev/kandev/internal/task/repository"
	ws "github.com/kandev/kandev/pkg/websocket"
)

type recordingTaskStopper struct {
	result orchestrator.CoordinatorTaskStopResult
	err    error
	calls  []string
}

func (s *recordingTaskStopper) StopTaskForCoordinator(
	_ context.Context,
	taskID string,
) (orchestrator.CoordinatorTaskStopResult, error) {
	s.calls = append(s.calls, taskID)
	return s.result, s.err
}

func stopTaskTestHandler(
	t *testing.T,
	tasks map[string]*models.Task,
	errorsByID map[string]error,
	stopper TaskStopper,
) *Handlers {
	return &Handlers{
		taskStopper: stopper,
		stopTaskGetter: func(_ context.Context, taskID string) (*models.Task, error) {
			if err := errorsByID[taskID]; err != nil {
				return nil, err
			}
			task, ok := tasks[taskID]
			if !ok {
				return nil, taskrepo.ErrTaskNotFound
			}
			return task, nil
		},
		logger: testLogger(t),
	}
}

func TestHandleStopTask_AuthorizesOnlyDirectParentInWorkspace(t *testing.T) {
	tasks := map[string]*models.Task{
		"grandparent": {ID: "grandparent", WorkspaceID: "ws-1"},
		"parent":      {ID: "parent", WorkspaceID: "ws-1", ParentID: "grandparent"},
		"child":       {ID: "child", WorkspaceID: "ws-1", ParentID: "parent"},
		"sibling":     {ID: "sibling", WorkspaceID: "ws-1", ParentID: "grandparent"},
		"unrelated":   {ID: "unrelated", WorkspaceID: "ws-1"},
		"cross-child": {ID: "cross-child", WorkspaceID: "ws-2", ParentID: "parent"},
	}
	tests := []struct {
		name       string
		senderID   string
		targetID   string
		wantStatus orchestrator.CoordinatorTaskStopStatus
		wantCode   string
	}{
		{name: "direct parent", senderID: "parent", targetID: "child", wantStatus: orchestrator.CoordinatorTaskStopStatusStopped},
		{name: "direct parent incomplete receipt", senderID: "parent", targetID: "child", wantStatus: orchestrator.CoordinatorTaskStopStatusIncomplete},
		{name: "self", senderID: "parent", targetID: "parent", wantCode: ws.ErrorCodeForbidden},
		{name: "sibling", senderID: "parent", targetID: "sibling", wantCode: ws.ErrorCodeForbidden},
		{name: "child", senderID: "child", targetID: "parent", wantCode: ws.ErrorCodeForbidden},
		{name: "grandparent", senderID: "grandparent", targetID: "child", wantCode: ws.ErrorCodeForbidden},
		{name: "unrelated", senderID: "unrelated", targetID: "child", wantCode: ws.ErrorCodeForbidden},
		{name: "cross workspace", senderID: "parent", targetID: "cross-child", wantCode: ws.ErrorCodeForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stopper := &recordingTaskStopper{result: orchestrator.CoordinatorTaskStopResult{Status: tt.wantStatus}}
			h := stopTaskTestHandler(t, tasks, nil, stopper)
			msg := makeWSMessage(t, ws.ActionMCPStopTask, map[string]interface{}{
				"task_id":        tt.targetID,
				"sender_task_id": tt.senderID,
				"reason":         "forged destructive cleanup reason",
				"force":          true,
			})

			resp, err := h.handleStopTask(context.Background(), msg)
			if err != nil {
				t.Fatalf("handleStopTask: %v", err)
			}
			if tt.wantCode != "" {
				assertWSError(t, resp, tt.wantCode)
				if len(stopper.calls) != 0 {
					t.Fatalf("forbidden request invoked stopper: %v", stopper.calls)
				}
				return
			}
			var payload struct {
				TaskID string                                 `json:"task_id"`
				Status orchestrator.CoordinatorTaskStopStatus `json:"status"`
			}
			if err := resp.ParsePayload(&payload); err != nil {
				t.Fatalf("parse response: %v", err)
			}
			if payload.TaskID != tt.targetID || payload.Status != tt.wantStatus {
				t.Fatalf("response = %#v", payload)
			}
			if len(stopper.calls) != 1 || stopper.calls[0] != tt.targetID {
				t.Fatalf("stopper calls = %v", stopper.calls)
			}
		})
	}
}

func TestHandleStopTaskAutomationUsesTrustedCallerWithoutLookingUpSender(t *testing.T) {
	tasks := map[string]*models.Task{
		"automation-caller":       {ID: "automation-caller", WorkspaceID: "ws-1"},
		"automation-other-parent": {ID: "automation-other-parent", WorkspaceID: "ws-1"},
		"automation-child":        {ID: "automation-child", WorkspaceID: "ws-1", ParentID: "automation-caller"},
		"automation-sibling":      {ID: "automation-sibling", WorkspaceID: "ws-1", ParentID: "automation-other-parent"},
		"foreign-sender":          {ID: "foreign-sender", WorkspaceID: "ws-2"},
	}
	for _, tt := range []struct {
		name       string
		targetID   string
		wantDenied bool
		wantCode   string
	}{
		{name: "direct child", targetID: "automation-child"},
		{name: "sibling", targetID: "automation-sibling", wantDenied: true},
		{name: "caller lookup fails", targetID: "automation-child", wantDenied: true, wantCode: ws.ErrorCodeNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var lookups []string
			stopper := &recordingTaskStopper{result: orchestrator.CoordinatorTaskStopResult{
				Status: orchestrator.CoordinatorTaskStopStatusStopped,
			}}
			h := &Handlers{
				taskStopper: stopper,
				stopTaskGetter: func(_ context.Context, taskID string) (*models.Task, error) {
					lookups = append(lookups, taskID)
					if tt.name == "caller lookup fails" && taskID == "automation-caller" {
						return nil, taskrepo.ErrTaskNotFound
					}
					task, ok := tasks[taskID]
					if !ok {
						return nil, taskrepo.ErrTaskNotFound
					}
					return task, nil
				},
				logger: testLogger(t),
			}
			ctx := mcpscope.WithPrincipal(context.Background(), mcpscope.Principal{
				AutomationID: "automation-1", WorkspaceID: "ws-1",
				CallerTaskID: "automation-caller", CallerSessionID: "session-1",
				Surface: mcpprofile.SurfaceAutomation,
			})
			msg := makeWSMessage(t, ws.ActionMCPStopTask, map[string]interface{}{
				"task_id": tt.targetID, "sender_task_id": "untrusted-sender-value",
			})

			resp, err := h.handleStopTask(ctx, msg)
			if err != nil {
				t.Fatalf("handleStopTask: %v", err)
			}
			if tt.wantDenied {
				code := tt.wantCode
				if code == "" {
					code = ws.ErrorCodeForbidden
				}
				assertWSError(t, resp, code)
				if len(stopper.calls) != 0 {
					t.Fatalf("forbidden request invoked stopper: %v", stopper.calls)
				}
				return
			}
			if resp.Type != ws.MessageTypeResponse {
				t.Fatalf("response type = %q, want response", resp.Type)
			}
			if len(lookups) != 2 || lookups[0] != "automation-caller" || lookups[1] != tt.targetID {
				t.Fatalf("task lookups = %v, want caller and target", lookups)
			}
		})
	}
}

func TestHandleStopTaskReturnsDurableReceipt(t *testing.T) {
	tasks := map[string]*models.Task{
		"parent": {ID: "parent", WorkspaceID: "ws-1"},
		"child":  {ID: "child", WorkspaceID: "ws-1", ParentID: "parent"},
	}
	receipt := models.CoordinatorStopOperation{ID: "operation-1", TaskID: "child", SessionID: "session-1", Status: models.CoordinatorStopOperationStatusIncomplete}
	sessionFence := models.CoordinatorStopSessionFenceReceipt{TaskID: "child", SessionID: "session-2", Status: models.CoordinatorStopOperationStatusIncomplete}
	stopper := &recordingTaskStopper{result: orchestrator.CoordinatorTaskStopResult{
		Status: orchestrator.CoordinatorTaskStopStatusIncomplete, Receipts: []models.CoordinatorStopOperation{receipt},
		SessionFences: []models.CoordinatorStopSessionFenceReceipt{sessionFence},
	}}
	h := stopTaskTestHandler(t, tasks, nil, stopper)
	msg := makeWSMessage(t, ws.ActionMCPStopTask, map[string]interface{}{"task_id": "child", "sender_task_id": "parent"})
	resp, err := h.handleStopTask(context.Background(), msg)
	if err != nil {
		t.Fatalf("handleStopTask: %v", err)
	}
	var payload struct {
		Receipts      []models.CoordinatorStopOperation           `json:"receipts"`
		SessionFences []models.CoordinatorStopSessionFenceReceipt `json:"session_fences"`
	}
	if err := resp.ParsePayload(&payload); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if len(payload.Receipts) != 1 || payload.Receipts[0].ID != receipt.ID || payload.Receipts[0].Status != receipt.Status {
		t.Fatalf("receipts = %#v, want operation %q", payload.Receipts, receipt.ID)
	}
	if len(payload.SessionFences) != 1 || payload.SessionFences[0].SessionID != sessionFence.SessionID {
		t.Fatalf("session fences = %#v, want session %q", payload.SessionFences, sessionFence.SessionID)
	}
}

func TestHandleStopTask_ValidatesPayload(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
		code    string
	}{
		{name: "malformed", payload: []byte(`{"task_id":`), code: ws.ErrorCodeBadRequest},
		{name: "missing task", payload: []byte(`{"sender_task_id":"parent"}`), code: ws.ErrorCodeValidation},
		{name: "missing sender", payload: []byte(`{"task_id":"child"}`), code: ws.ErrorCodeValidation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := stopTaskTestHandler(t, nil, nil, &recordingTaskStopper{})
			msg := &ws.Message{ID: "request-1", Action: ws.ActionMCPStopTask, Payload: tt.payload}
			resp, err := h.handleStopTask(context.Background(), msg)
			if err != nil {
				t.Fatalf("handleStopTask: %v", err)
			}
			assertWSError(t, resp, tt.code)
		})
	}
}

func TestHandleStopTask_MapsLookupFailures(t *testing.T) {
	infraFailure := errors.New("database unavailable")
	tests := []struct {
		name       string
		errorsByID map[string]error
		wantCode   string
	}{
		{name: "sender missing", errorsByID: map[string]error{"parent": taskrepo.ErrTaskNotFound}, wantCode: ws.ErrorCodeNotFound},
		{name: "sender lookup failure", errorsByID: map[string]error{"parent": infraFailure}, wantCode: ws.ErrorCodeInternalError},
		{name: "target missing", errorsByID: map[string]error{"child": taskrepo.ErrTaskNotFound}, wantCode: ws.ErrorCodeNotFound},
		{name: "target lookup failure", errorsByID: map[string]error{"child": infraFailure}, wantCode: ws.ErrorCodeInternalError},
	}
	tasks := map[string]*models.Task{
		"parent": {ID: "parent", WorkspaceID: "ws-1"},
		"child":  {ID: "child", WorkspaceID: "ws-1", ParentID: "parent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stopper := &recordingTaskStopper{}
			h := stopTaskTestHandler(t, tasks, tt.errorsByID, stopper)
			msg := makeWSMessage(t, ws.ActionMCPStopTask, map[string]interface{}{
				"task_id": "child", "sender_task_id": "parent",
			})

			resp, err := h.handleStopTask(context.Background(), msg)
			if err != nil {
				t.Fatalf("handleStopTask: %v", err)
			}
			assertWSError(t, resp, tt.wantCode)
			if len(stopper.calls) != 0 {
				t.Fatalf("lookup failure invoked stopper: %v", stopper.calls)
			}
		})
	}
}

func TestHandleStopTask_MapsStopperFailure(t *testing.T) {
	tasks := map[string]*models.Task{
		"parent": {ID: "parent", WorkspaceID: "ws-1"},
		"child":  {ID: "child", WorkspaceID: "ws-1", ParentID: "parent"},
	}
	stopper := &recordingTaskStopper{err: errors.New("stop failed")}
	h := stopTaskTestHandler(t, tasks, nil, stopper)
	msg := makeWSMessage(t, ws.ActionMCPStopTask, map[string]interface{}{
		"task_id": "child", "sender_task_id": "parent",
	})

	resp, err := h.handleStopTask(context.Background(), msg)
	if err != nil {
		t.Fatalf("handleStopTask: %v", err)
	}
	assertWSError(t, resp, ws.ErrorCodeInternalError)
}

func TestHandleStopTask_ReturnsNotRunning(t *testing.T) {
	tasks := map[string]*models.Task{
		"parent": {ID: "parent", WorkspaceID: "ws-1"},
		"child":  {ID: "child", WorkspaceID: "ws-1", ParentID: "parent"},
	}
	stopper := &recordingTaskStopper{result: orchestrator.CoordinatorTaskStopResult{
		Status: orchestrator.CoordinatorTaskStopStatusNotRunning,
	}}
	h := stopTaskTestHandler(t, tasks, nil, stopper)
	msg := makeWSMessage(t, ws.ActionMCPStopTask, map[string]interface{}{
		"task_id": "child", "sender_task_id": "parent",
	})

	resp, err := h.handleStopTask(context.Background(), msg)
	if err != nil {
		t.Fatalf("handleStopTask: %v", err)
	}
	var payload struct {
		Status orchestrator.CoordinatorTaskStopStatus `json:"status"`
	}
	if err := resp.ParsePayload(&payload); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if payload.Status != orchestrator.CoordinatorTaskStopStatusNotRunning {
		t.Fatalf("status = %q, want not_running", payload.Status)
	}
}

func TestHandleStopTask_RejectsMissingStopperAndInvalidStatus(t *testing.T) {
	tasks := map[string]*models.Task{
		"parent": {ID: "parent", WorkspaceID: "ws-1"},
		"child":  {ID: "child", WorkspaceID: "ws-1", ParentID: "parent"},
	}
	tests := []struct {
		name    string
		stopper TaskStopper
	}{
		{name: "missing stopper"},
		{name: "invalid status", stopper: &recordingTaskStopper{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := stopTaskTestHandler(t, tasks, nil, tt.stopper)
			msg := makeWSMessage(t, ws.ActionMCPStopTask, map[string]interface{}{
				"task_id": "child", "sender_task_id": "parent",
			})
			resp, err := h.handleStopTask(context.Background(), msg)
			if err != nil {
				t.Fatalf("handleStopTask: %v", err)
			}
			assertWSError(t, resp, ws.ErrorCodeInternalError)
		})
	}
}

func TestRegisterHandlers_RegistersStopTask(t *testing.T) {
	tasks := map[string]*models.Task{
		"parent": {ID: "parent", WorkspaceID: "ws-1"},
		"child":  {ID: "child", WorkspaceID: "ws-1", ParentID: "parent"},
	}
	stopper := &recordingTaskStopper{result: orchestrator.CoordinatorTaskStopResult{
		Status: orchestrator.CoordinatorTaskStopStatusStopped,
	}}
	h := stopTaskTestHandler(t, tasks, nil, stopper)
	dispatcher := ws.NewDispatcher()
	h.RegisterHandlers(dispatcher)
	msg := makeWSMessage(t, ws.ActionMCPStopTask, map[string]interface{}{
		"task_id": "child", "sender_task_id": "parent",
	})

	resp, err := dispatcher.Dispatch(context.Background(), msg)
	if err != nil {
		t.Fatalf("dispatch stop task: %v", err)
	}
	if resp == nil || resp.Type != ws.MessageTypeResponse {
		t.Fatalf("dispatch response = %#v", resp)
	}
	if len(stopper.calls) != 1 || stopper.calls[0] != "child" {
		t.Fatalf("stopper calls = %v", stopper.calls)
	}
}
