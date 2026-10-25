package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/kandev/kandev/internal/agentctl/types/streams"
	mcpprofile "github.com/kandev/kandev/internal/mcp/profile"
	mcpscope "github.com/kandev/kandev/internal/mcp/scope"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	ws "github.com/kandev/kandev/pkg/websocket"
)

type activeQueueCensusReader interface {
	GetActiveQueueCensus(context.Context) (*messagequeue.ActiveQueueCensus, error)
}

func (h *Handlers) handleGetMessageQueueCensus(ctx context.Context, msg *ws.Message) (*ws.Message, error) {
	principal, ok := mcpscope.PrincipalFromContext(ctx)
	execution, hasExecution := streams.MCPExecutionContextFromContext(ctx)
	if !ok || !hasExecution || execution.ExecutionID == "" ||
		(principal.Surface != mcpprofile.SurfaceKanbanTask && principal.Surface != mcpprofile.SurfaceOfficeTask) ||
		principal.CallerTaskID != execution.TaskID || principal.CallerSessionID != execution.SessionID || principal.WorkspaceID == "" {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeNotFound, "queue census unavailable", nil)
	}
	if !emptyCensusObject(msg.Payload) {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeBadRequest, "queue census requires an empty object", nil)
	}
	reader, ok := h.sessionLauncher.(activeQueueCensusReader)
	if !ok {
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeUnavailable, "queue census unavailable", nil)
	}
	result, err := reader.GetActiveQueueCensus(ctx)
	if err != nil {
		if errors.Is(err, messagequeue.ErrCensusTooLarge) {
			return ws.NewError(msg.ID, msg.Action, "RESOURCE_EXHAUSTED", "queue census limit exceeded", nil)
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ws.NewError(msg.ID, msg.Action, "CANCELLED", "queue census cancelled", nil)
		}
		return ws.NewError(msg.ID, msg.Action, ws.ErrorCodeUnavailable, "queue census unavailable", nil)
	}
	return ws.NewResponse(msg.ID, msg.Action, result)
}

func emptyCensusObject(raw json.RawMessage) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return false
	}
	last, err := decoder.Token()
	if err != nil || last != json.Delim('}') {
		return false
	}
	_, err = decoder.Token()
	return errors.Is(err, io.EOF)
}
