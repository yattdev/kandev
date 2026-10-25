package handlers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	mcpprofile "github.com/kandev/kandev/internal/mcp/profile"
	mcpscope "github.com/kandev/kandev/internal/mcp/scope"
	ws "github.com/kandev/kandev/pkg/websocket"
)

func TestDependencyCallerRejectsRawPeerMutationFromTaskPrincipal(t *testing.T) {
	h := &Handlers{}
	request := &dependencyMutationPayload{TaskID: "peer-task", DependsOnTaskID: "predecessor"}
	message := makeWSMessage(t, ws.ActionMCPAddTaskDependency, map[string]interface{}{})
	for _, surface := range []mcpprofile.Surface{mcpprofile.SurfaceKanbanTask, mcpprofile.SurfaceOfficeTask} {
		ctx := mcpscope.WithPrincipal(context.Background(), mcpscope.Principal{
			WorkspaceID: "workspace", CallerTaskID: "own-task", CallerSessionID: "session", Surface: surface,
		})
		assertWSError(t, h.authorizeDependencyCaller(ctx, message, request), ws.ErrorCodeNotFound)
		request.TaskID = "own-task"
		require.Nil(t, h.authorizeDependencyCaller(ctx, message, request))
		request.TaskID = "peer-task"
	}
	require.Nil(t, h.authorizeDependencyCaller(context.Background(), message, request),
		"external callers retain their authenticated user path")
}
