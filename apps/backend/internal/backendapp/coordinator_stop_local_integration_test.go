package backendapp

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/agent/agents"
	"github.com/kandev/kandev/internal/agent/registry"
	agentctl "github.com/kandev/kandev/internal/agent/runtime/agentctl"
	"github.com/kandev/kandev/internal/agent/runtime/lifecycle"
	ctlapi "github.com/kandev/kandev/internal/agentctl/server/api"
	ctlconfig "github.com/kandev/kandev/internal/agentctl/server/config"
	"github.com/kandev/kandev/internal/agentctl/server/instance"
	"github.com/kandev/kandev/internal/agentctl/server/process"
	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/events/bus"
	mcphandlers "github.com/kandev/kandev/internal/mcp/handlers"
	mcpserver "github.com/kandev/kandev/internal/mcp/server"
	"github.com/kandev/kandev/internal/orchestrator"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/pkg/agent"
	ws "github.com/kandev/kandev/pkg/websocket"
)

type localStopProfile struct{}

func (localStopProfile) ResolveProfile(context.Context, string) (*lifecycle.AgentProfileInfo, error) {
	return &lifecycle.AgentProfileInfo{ProfileID: "local-stop-profile", AgentName: "mock-agent", Model: "mock-fast"}, nil
}

func TestCoordinatorStopLocalMCPProductionTerminalProof(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	harness := newBootStateTestHarness(t)
	log := newTestLogger()
	events := bus.NewMemoryEventBus(log)
	workspace := t.TempDir()
	binary := filepath.Join(t.TempDir(), "mock-agent")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/mock-agent")
	build.Dir = filepath.Clean("../..")
	output, err := build.CombinedOutput()
	require.NoError(t, err, "build owned ACP process: %s", output)

	portProbe, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	instancePort := portProbe.Addr().(*net.TCPAddr).Port
	require.NoError(t, portProbe.Close())
	control := instance.NewManager(&ctlconfig.Config{Ports: ctlconfig.PortConfig{Base: instancePort, Max: instancePort}, Defaults: ctlconfig.InstanceDefaults{Protocol: agent.ProtocolACP}}, log)
	processes := make(chan *process.Manager, 2)
	control.SetServerFactory(func(cfg *ctlconfig.InstanceConfig, pm *process.Manager, log *logger.Logger) http.Handler {
		processes <- pm
		return ctlapi.NewServer(cfg, pm, nil, nil, log).Router()
	})
	t.Cleanup(func() { require.NoError(t, control.Shutdown(context.Background())) })
	controlHTTP := httptest.NewServer(ctlapi.NewControlServer(&ctlconfig.Config{}, control, log).Router())
	t.Cleanup(controlHTTP.Close)
	host, portText, err := net.SplitHostPort(strings.TrimPrefix(controlHTTP.URL, "http://"))
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	backends := lifecycle.NewExecutorRegistry(log)
	backends.Register(lifecycle.NewStandaloneExecutor(agentctl.NewControlClient(host, port, log), host, port, log))
	agentsRegistry := registry.NewRegistry(log)
	mock := agents.NewMockAgent()
	mock.SetEnabled(true)
	mock.SetBinaryPath(binary)
	mock.SetSupportsMCP(false)
	require.NoError(t, agentsRegistry.Register(mock))
	mgr := lifecycle.NewManager(agentsRegistry, events, backends, nil, localStopProfile{}, nil, lifecycle.ExecutorFallbackWarn, t.TempDir(), log)
	mgr.SetExecutorRunningWriter(harness.taskRepo)
	t.Cleanup(func() { require.NoError(t, mgr.Stop()) })
	require.NoError(t, harness.taskRepo.CreateWorkspace(ctx, &models.Workspace{ID: "local-stop-workspace", Name: "owned stop proof"}))
	require.NoError(t, harness.taskRepo.CreateTask(ctx, &models.Task{ID: "local-stop-parent", Title: "parent", WorkspaceID: "local-stop-workspace"}))
	require.NoError(t, harness.taskRepo.CreateTask(ctx, &models.Task{ID: "local-stop-child", Title: "child", ParentID: "local-stop-parent", WorkspaceID: "local-stop-workspace"}))
	require.NoError(t, harness.taskRepo.CreateTaskSession(ctx, &models.TaskSession{ID: "local-parent-session", TaskID: "local-stop-parent", State: models.TaskSessionStateRunning}))
	require.NoError(t, harness.taskRepo.CreateTaskSession(ctx, &models.TaskSession{ID: "local-stop-session", TaskID: "local-stop-child", State: models.TaskSessionStateRunning, AgentProfileID: "local-stop-profile"}))
	now := time.Now().UTC()
	require.NoError(t, harness.taskRepo.CreateTurn(ctx, &models.Turn{ID: "local-stop-turn", TaskID: "local-stop-child", TaskSessionID: "local-stop-session", StartedAt: now, CreatedAt: now, UpdatedAt: now}))
	execution, err := mgr.Launch(ctx, &lifecycle.LaunchRequest{TaskID: "local-stop-child", SessionID: "local-stop-session", AgentProfileID: "local-stop-profile", WorkspacePath: workspace, ExecutorType: "local"})
	require.NoError(t, err)
	proc := <-processes
	require.NoError(t, mgr.StartAgentProcess(ctx, execution.ID))
	require.NotNil(t, proc)
	require.Equal(t, process.StatusRunning, proc.Status())
	running, err := harness.taskRepo.GetExecutorRunningBySessionID(ctx, "local-stop-session")
	require.NoError(t, err)
	require.NotZero(t, running.AgentctlGeneration)

	// A FIFO is a deterministic admitted-command boundary owned by this test.
	fifo := filepath.Join(workspace, "release-admitted")
	require.NoError(t, exec.CommandContext(ctx, "mkfifo", fifo).Run())
	marker := filepath.Join(workspace, "admitted-finished")
	_, err = proc.StartProcess(ctx, process.StartProcessRequest{SessionID: "local-stop-session", Command: "read value < " + fifo + "; touch " + marker})
	require.NoError(t, err)
	require.Len(t, proc.ListProcesses(""), 1)
	release, err := proc.BeginAdapterOperation()
	require.NoError(t, err)
	t.Cleanup(release)
	adapter := newLifecycleAdapter(mgr, agentsRegistry, log)
	svc := orchestrator.NewService(orchestrator.DefaultServiceConfig(), events, adapter, &taskRepositoryAdapter{repo: harness.taskRepo, svc: harness.taskSvc}, harness.taskRepo, nil, nil, nil, log)
	dispatcher := ws.NewDispatcher()
	mcphandlers.NewHandlers(harness.taskSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, svc, svc.GetMessageQueue(), log).RegisterHandlers(dispatcher)
	mcp := mcpserver.New(mcpserver.NewDispatcherBackendClient(dispatcher, log), "local-parent-session", "local-stop-parent", 0, log, "", true, mcpserver.ModeTask)
	router := gin.New()
	mcp.RegisterRoutes(router)
	mcpHTTP := httptest.NewServer(router)
	t.Cleanup(mcpHTTP.Close)
	sessionID, _ := localStopRPC(t, ctx, mcpHTTP.URL, "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"owned-stop-proof","version":"1"}}}`)
	result := make(chan localStopRPCResult, 1)
	go func() {
		_, body, err := localStopRPCCall(ctx, mcpHTTP.URL, sessionID, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"stop_task_kandev","arguments":{"task_id":"local-stop-child","operation_id":"owned-stop-proof"}}}`)
		result <- localStopRPCResult{body: body, err: err}
	}()
	var earlyResponse *localStopRPCResult
	require.Eventually(t, func() bool {
		select {
		case response := <-result:
			earlyResponse = &response
			return true
		default:
		}
		probeRelease, err := proc.BeginAdapterOperation()
		if err == nil {
			probeRelease()
			return false
		}
		return err == process.ErrManagerStopping
	}, 3*time.Second, time.Millisecond)
	require.Nil(t, earlyResponse, "MCP returned before fencing: %+v", earlyResponse)
	_, err = proc.StartProcess(ctx, process.StartProcessRequest{SessionID: "local-stop-session", Command: "touch " + filepath.Join(workspace, "late-command")})
	require.ErrorIs(t, err, process.ErrManagerStopping)
	select {
	case <-result:
		t.Fatal("stopped response before admitted operation drain")
	default:
	}
	require.NoError(t, exec.CommandContext(ctx, "sh", "-c", "printf done > "+fifo).Run())
	require.Eventually(t, func() bool { _, err := os.Stat(marker); return err == nil && len(proc.ListProcesses("")) == 0 }, 3*time.Second, time.Millisecond)
	release()
	select {
	case response := <-result:
		require.NoError(t, response.err)
		require.NotContains(t, response.body, `"isError":true`)
	case <-ctx.Done():
		t.Fatal("real stop did not finish after owned admission drain")
	}
	receipt, err := svc.GetCoordinatorStopReceipt(ctx, "local-stop-child", "local-stop-parent", "owned-stop-proof")
	require.NoError(t, err)
	require.Equal(t, orchestrator.CoordinatorTaskStopStatusStopped, receipt.Status)
	require.Len(t, receipt.Receipts, 1)
	require.Equal(t, execution.ID, receipt.Receipts[0].ExecutionID)
	require.Equal(t, running.AgentctlGeneration, receipt.Receipts[0].AgentctlGeneration)
	require.False(t, receipt.Receipts[0].AdmissionCutoff.IsZero())
	require.Equal(t, "exact_executor_terminal", receipt.Receipts[0].ProofScope)
	session, err := harness.taskRepo.GetTaskSession(ctx, "local-stop-session")
	require.NoError(t, err)
	require.Equal(t, models.TaskSessionStateCancelled, session.State)
	turn, err := harness.taskRepo.GetTurn(ctx, "local-stop-turn")
	require.NoError(t, err)
	require.NotNil(t, turn.CompletedAt)
	_, err = harness.taskRepo.GetExecutorRunningBySessionID(ctx, "local-stop-session")
	require.ErrorIs(t, err, models.ErrExecutorRunningNotFound)
	require.Equal(t, process.StatusStopped, proc.Status())
	require.Empty(t, proc.ListProcesses(""))
	_, err = os.Stat(filepath.Join(workspace, "late-command"))
	require.True(t, os.IsNotExist(err))

	require.NoError(t, harness.taskRepo.UpdateTaskSessionState(ctx, "local-stop-session", models.TaskSessionStateStarting, ""))
	require.NoError(t, harness.taskRepo.CreateTurn(ctx, &models.Turn{ID: "local-successor-turn", TaskID: "local-stop-child", TaskSessionID: "local-stop-session", StartedAt: time.Now().UTC()}))
	successor, err := mgr.Launch(ctx, &lifecycle.LaunchRequest{TaskID: "local-stop-child", SessionID: "local-stop-session", AgentProfileID: "local-stop-profile", WorkspacePath: workspace, ExecutorType: "local"})
	require.NoError(t, err)
	require.NotEqual(t, execution.ID, successor.ID)
	proc = <-processes
	require.NoError(t, mgr.StartAgentProcess(ctx, successor.ID))
	successorRow, err := harness.taskRepo.GetExecutorRunningBySessionID(ctx, "local-stop-session")
	require.NoError(t, err)
	successorSession, err := harness.taskRepo.GetTaskSession(ctx, "local-stop-session")
	require.NoError(t, err)
	_, replay := localStopRPC(t, ctx, mcpHTTP.URL, sessionID, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"stop_task_kandev","arguments":{"task_id":"local-stop-child","operation_id":"owned-stop-proof"}}}`)
	require.NotContains(t, replay, `"isError":true`)
	currentRow, err := harness.taskRepo.GetExecutorRunningBySessionID(ctx, "local-stop-session")
	require.NoError(t, err)
	require.Equal(t, successorRow, currentRow)
	currentSession, err := harness.taskRepo.GetTaskSession(ctx, "local-stop-session")
	require.NoError(t, err)
	require.Equal(t, successorSession, currentSession)
	successorTurn, err := harness.taskRepo.GetTurn(ctx, "local-successor-turn")
	require.NoError(t, err)
	require.Nil(t, successorTurn.CompletedAt)
	require.Equal(t, process.StatusRunning, proc.Status())
}

func localStopRPC(t *testing.T, ctx context.Context, baseURL, sessionID, body string) (string, string) {
	t.Helper()
	id, result, err := localStopRPCCall(ctx, baseURL, sessionID, body)
	require.NoError(t, err)
	return id, result
}

type localStopRPCResult struct {
	body string
	err  error
}

func localStopRPCCall(ctx context.Context, baseURL, sessionID, body string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/mcp", strings.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()
	var lines strings.Builder
	_, err = io.Copy(&lines, resp.Body)
	if err != nil {
		return "", "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("MCP status %d: %s", resp.StatusCode, lines.String())
	}
	return resp.Header.Get("Mcp-Session-Id"), lines.String(), nil
}
