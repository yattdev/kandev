package backendapp

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/agent/agents"
	"github.com/kandev/kandev/internal/agent/registry"
	agentctl "github.com/kandev/kandev/internal/agent/runtime/agentctl"
	"github.com/kandev/kandev/internal/agent/runtime/lifecycle"
	ctlapi "github.com/kandev/kandev/internal/agentctl/server/api"
	ctlconfig "github.com/kandev/kandev/internal/agentctl/server/config"
	"github.com/kandev/kandev/internal/agentctl/server/instance"
	"github.com/kandev/kandev/internal/agentctl/server/process"
	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/events/bus"
	"github.com/kandev/kandev/internal/orchestrator"
	"github.com/kandev/kandev/internal/task/models"
	taskrepo "github.com/kandev/kandev/internal/task/repository/sqlite"
	taskservice "github.com/kandev/kandev/internal/task/service"
	"github.com/kandev/kandev/pkg/agent"
	"github.com/stretchr/testify/require"
)

func TestCoordinatorStopRealServiceRestartResumesDurableBoundaries(t *testing.T) {
	for _, boundary := range []string{"captured request", "settled turn intent", "lost fence acknowledgement", "terminal process missing proof", "terminal proof missing final receipt", "changed generation", "unknown generation", "canceled admission", "deadline admission"} {
		t.Run(boundary, func(t *testing.T) { assertCoordinatorRealRestart(t, boundary) })
	}
}

func assertCoordinatorRealRestart(t *testing.T, boundary string) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	h := newBootStateTestHarness(t)
	log := newTestLogger()
	workspace, binary := t.TempDir(), filepath.Join(t.TempDir(), "mock-agent")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/mock-agent")
	build.Dir = "../.."
	output, err := build.CombinedOutput()
	require.NoError(t, err, "%s", output)
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	instancePort := probe.Addr().(*net.TCPAddr).Port
	require.NoError(t, probe.Close())
	const fixtureAuthToken = "restart-fixture-token"
	controlConfig := &ctlconfig.Config{AuthToken: fixtureAuthToken, Ports: ctlconfig.PortConfig{Base: instancePort, Max: instancePort}, Defaults: ctlconfig.InstanceDefaults{Protocol: agent.ProtocolACP}}
	control := instance.NewManager(controlConfig, log)
	processes := make(chan *process.Manager, 1)
	control.SetServerFactory(func(cfg *ctlconfig.InstanceConfig, pm *process.Manager, log *logger.Logger) http.Handler {
		processes <- pm
		return ctlapi.NewServer(cfg, pm, nil, nil, log).Router()
	})
	t.Cleanup(func() { require.NoError(t, control.Shutdown(context.Background())) })
	controlHTTP := httptest.NewServer(ctlapi.NewControlServer(controlConfig, control, log).Router())
	t.Cleanup(controlHTTP.Close)
	host, portText, err := net.SplitHostPort(strings.TrimPrefix(controlHTTP.URL, "http://"))
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	reg := registry.NewRegistry(log)
	mock := agents.NewMockAgent()
	mock.SetEnabled(true)
	mock.SetBinaryPath(binary)
	mock.SetSupportsMCP(false)
	require.NoError(t, reg.Register(mock))
	runtimeDataDir := t.TempDir()
	newManager := func(repo *taskrepo.Repository) *lifecycle.Manager {
		backends := lifecycle.NewExecutorRegistry(log)
		client := agentctl.NewControlClient(host, port, log)
		client.SetAuthToken(fixtureAuthToken)
		standalone := lifecycle.NewStandaloneExecutor(client, host, port, log)
		standalone.SetAuthToken(fixtureAuthToken)
		backends.Register(standalone)
		mgr := lifecycle.NewManager(reg, bus.NewMemoryEventBus(log), backends, nil, localStopProfile{}, nil, lifecycle.ExecutorFallbackWarn, runtimeDataDir, log)
		mgr.SetExecutorRunningWriter(repo)
		mgr.SetExecutorProfileReader(repo)
		mgr.SetSessionSettingsSnapshotWriter(repo)
		mgr.SetAgentDeliveryRepository(repo)
		mgr.SetAgentSurvivalEnabled(true)
		return mgr
	}
	require.NoError(t, h.taskRepo.CreateWorkspace(ctx, &models.Workspace{ID: "restart-workspace", Name: "owned restart proof"}))
	require.NoError(t, h.taskRepo.CreateTask(ctx, &models.Task{ID: "restart-parent", Title: "parent", WorkspaceID: "restart-workspace"}))
	require.NoError(t, h.taskRepo.CreateTask(ctx, &models.Task{ID: "restart-child", Title: "child", ParentID: "restart-parent", WorkspaceID: "restart-workspace"}))
	require.NoError(t, h.taskRepo.CreateTaskSession(ctx, &models.TaskSession{ID: "restart-session", TaskID: "restart-child", State: models.TaskSessionStateRunning, AgentProfileID: "local-stop-profile", QueueIncarnationID: "restart-incarnation"}))
	require.NoError(t, h.taskRepo.CreateTurn(ctx, &models.Turn{ID: "restart-turn", TaskID: "restart-child", TaskSessionID: "restart-session", StartedAt: time.Now().UTC()}))
	const deliveryIncarnation = "restart-incarnation"
	require.NoError(t, h.taskRepo.CreateHarnessSessionGeneration(ctx, &models.HarnessSessionGeneration{
		SessionID: "restart-session", IncarnationID: deliveryIncarnation, Generation: 1, CreationReason: "initial",
	}))
	mgr := newManager(h.taskRepo)
	execution, err := mgr.Launch(ctx, &lifecycle.LaunchRequest{
		TaskID: "restart-child", SessionID: "restart-session", AgentProfileID: "local-stop-profile", WorkspacePath: workspace, ExecutorType: "local",
		DeliveryStreamID: "restart-session", DeliveryIncarnationID: deliveryIncarnation, DeliveryHarnessGeneration: 1,
	})
	require.NoError(t, err)
	pm := <-processes
	require.NoError(t, mgr.StartAgentProcess(ctx, execution.ID))
	running, err := h.taskRepo.GetExecutorRunningBySessionID(ctx, "restart-session")
	require.NoError(t, err)
	request := models.CoordinatorStopRequest{TaskID: "restart-child", ParentTaskID: "restart-parent", OperationID: "persisted-restart"}
	_, candidates, _, err := h.taskRepo.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
	require.NoError(t, err)
	if boundary != "captured request" && boundary != "changed generation" && boundary != "unknown generation" {
		_, _, err := h.taskRepo.CaptureCoordinatorStopOperation(ctx, models.CoordinatorStopOperation{ID: "restart-intent", RequestOperationID: request.OperationID, TaskID: request.TaskID, SessionID: "restart-session", TurnID: "restart-turn", ExecutionID: execution.ID, AgentctlGeneration: running.AgentctlGeneration, ExecutorStatus: running.Status, ExecutorUpdatedAt: running.UpdatedAt})
		require.NoError(t, err)
	}
	if boundary == "lost fence acknowledgement" || boundary == "terminal process missing proof" {
		_, err := execution.CloseExecutionAdmission(ctx, running.AgentctlGeneration)
		require.NoError(t, err)
	}
	if boundary == "terminal process missing proof" {
		require.NoError(t, execution.StopAgentctlGracefully(ctx, running.AgentctlGeneration))
	}
	if boundary == "terminal proof missing final receipt" {
		require.NoError(t, mgr.StopExecutionWithFence(ctx, execution.ID, running.AgentctlGeneration, "owned restart proof", func(receipt *lifecycle.ExecutionFenceReceipt) error {
			_, err := h.taskRepo.ConsumeCoordinatorStopFenceReceipt(ctx, "restart-intent", models.CoordinatorStopFenceReceipt{ExecutionID: receipt.ExecutionID, AgentctlGeneration: receipt.AgentctlGeneration, AdmissionClosedAt: receipt.AdmissionClosedAt, AdmissionCutoffSequence: receipt.AdmissionCutoffSequence, ManagedProcessesDrained: receipt.ManagedProcessesDrained})
			return err
		}, func() error {
			return h.taskRepo.RecordCoordinatorStopLifecycleProof(ctx, "restart-intent", execution.ID, running.AgentctlGeneration)
		}))
	}
	if boundary == "changed generation" {
		_, err := control.UpdateExecutionGeneration(ctx, execution.ID, instance.ExecutionGenerationUpdate{ExecutionID: execution.ID, ExpectedAgentctlGeneration: running.AgentctlGeneration, AgentctlGeneration: running.AgentctlGeneration + 1})
		require.NoError(t, err)
	}
	if boundary == "unknown generation" {
		running.AgentctlGeneration = 0
		// The original captured candidate remains immutable; only durable runtime knowledge is lost.
		_, err := h.db.ExecContext(ctx, "UPDATE executors_running SET agentctl_generation = 0 WHERE session_id = ?", "restart-session")
		require.NoError(t, err)
	}
	require.NoError(t, mgr.StopAllAgents(ctx), "detach owned execution before backend shutdown")
	require.NoError(t, mgr.Stop(), "stop backend subscriptions after detaching execution")
	if boundary == "terminal process missing proof" || boundary == "terminal proof missing final receipt" {
		require.Equal(t, process.StatusStopped, pm.Status(), "terminal process stays stopped across backend restart")
	} else {
		require.Equal(t, process.StatusRunning, pm.Status(), "owned agentctl process survives backend restart")
	}
	var dbPath string
	require.NoError(t, h.db.QueryRowContext(ctx, "SELECT file FROM pragma_database_list WHERE name = 'main'").Scan(&dbPath))
	require.NoError(t, h.db.Close(), "original persistence connection ends before reconstruction")
	conn, err := db.OpenSQLite(dbPath)
	require.NoError(t, err)
	reopenedDB := sqlx.NewDb(conn, "sqlite3")
	t.Cleanup(func() { require.NoError(t, reopenedDB.Close()) })
	reopened, err := taskrepo.NewWithDB(reopenedDB, reopenedDB, nil)
	require.NoError(t, err)
	_, saved, _, err := reopened.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
	require.NoError(t, err)
	require.Equal(t, candidates, saved)
	recovered := newManager(reopened)
	t.Cleanup(func() { require.NoError(t, recovered.Stop()) })
	if boundary == "changed generation" || boundary == "unknown generation" {
		require.Equal(t, process.StatusRunning, pm.Status(), "runtime before lifecycle reconstruction")
	}
	require.NoError(t, recovered.Start(ctx))
	if boundary == "captured request" || boundary == "settled turn intent" {
		postRecoveryRow, rowErr := reopened.GetExecutorRunningBySessionID(ctx, "restart-session")
		require.NoError(t, rowErr)
		require.Equal(t, running.AgentctlGeneration, postRecoveryRow.AgentctlGeneration,
			"settings-source recovery must preserve the retained agentctl generation")
	}
	if boundary == "changed generation" || boundary == "unknown generation" {
		require.Equal(t, process.StatusRunning, pm.Status(), "runtime after lifecycle reconstruction")
	}
	svc := orchestrator.NewService(orchestrator.DefaultServiceConfig(), bus.NewMemoryEventBus(log), newLifecycleAdapter(recovered, reg, log), &taskRepositoryAdapter{repo: reopened, svc: newCoordinatorRestartTaskService(reopened, log)}, reopened, nil, nil, nil, log)
	svc.SetTurnService(newTurnServiceAdapter(newCoordinatorRestartTaskService(reopened, log)))
	svc.SetRetrackedSessionChecker(recovered.WasSessionRetracked)
	if boundary == "canceled admission" || boundary == "deadline admission" {
		assertCoordinatorRestartAdmissionCancellation(t, ctx, svc, reopened, pm, request, candidates, boundary == "deadline admission")
	}
	startErr := svc.Start(ctx)
	if boundary == "unknown generation" {
		require.ErrorContains(t, startErr, "identity")
	} else {
		require.NoError(t, startErr)
	}
	if startErr == nil {
		t.Cleanup(func() { require.NoError(t, svc.Stop()) })
	}
	result, err := svc.GetCoordinatorStopReceipt(ctx, request.TaskID, request.ParentTaskID, request.OperationID)
	if boundary == "changed generation" || boundary == "unknown generation" {
		if err == nil {
			require.Equal(t, orchestrator.CoordinatorTaskStopStatusIncomplete, result.Status)
		}
		require.Equal(t, process.StatusRunning, pm.Status())
		row, err := reopened.GetExecutorRunningBySessionID(ctx, "restart-session")
		require.NoError(t, err)
		require.Equal(t, execution.ID, row.AgentExecutionID)
		require.Equal(t, running.AgentctlGeneration, row.AgentctlGeneration)
		_, unchanged, _, err := reopened.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
		require.NoError(t, err)
		require.Equal(t, candidates, unchanged)
		return
	}
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		var retryErr error
		result, retryErr = svc.StopTaskForCoordinatorOperation(ctx, request.TaskID, request.ParentTaskID, request.OperationID)
		return retryErr == nil && result.Status == orchestrator.CoordinatorTaskStopStatusStopped
	}, 3*time.Second, time.Millisecond, "startup and original immutable retry must complete after transient runtime ownership is released")
	require.Equal(t, orchestrator.CoordinatorTaskStopStatusStopped, result.Status, "real startup must complete only original persisted identity")
	require.Len(t, result.Receipts, 1)
	require.Equal(t, execution.ID, result.Receipts[0].ExecutionID)
	require.Equal(t, running.AgentctlGeneration, result.Receipts[0].AgentctlGeneration)
	require.Equal(t, process.StatusStopped, pm.Status())
	require.Empty(t, pm.ListProcesses(""))
	turn, err := reopened.GetTurn(ctx, "restart-turn")
	require.NoError(t, err)
	require.NotNil(t, turn.CompletedAt)
	row, err := reopened.GetExecutorRunningBySessionID(ctx, "restart-session")
	if err == nil {
		require.Equal(t, execution.ID, row.AgentExecutionID)
		require.Equal(t, running.AgentctlGeneration, row.AgentctlGeneration)
		require.Contains(t, []string{models.ExecutorRunningStatusStopped, models.ExecutorRunningStatusComplete, models.ExecutorRunningStatusFailed}, row.Status)
	} else {
		require.ErrorIs(t, err, models.ErrExecutorRunningNotFound)
	}
	if boundary == "captured request" {
		assertCoordinatorRestartSuccessor(t, ctx, reopened, recovered, svc, processes, workspace)
	}
}

func assertCoordinatorRestartSuccessor(t *testing.T, ctx context.Context, repo *taskrepo.Repository, mgr *lifecycle.Manager, svc *orchestrator.Service, processes <-chan *process.Manager, workspace string) {
	require.NoError(t, repo.UpdateTaskSessionState(ctx, "restart-session", models.TaskSessionStateStarting, ""))
	require.NoError(t, repo.CreateTurn(ctx, &models.Turn{ID: "restart-successor", TaskID: "restart-child", TaskSessionID: "restart-session", StartedAt: time.Now().UTC()}))
	require.NoError(t, repo.CreateHarnessSessionGeneration(ctx, &models.HarnessSessionGeneration{
		SessionID: "restart-session", IncarnationID: "restart-incarnation", Generation: 2, PredecessorGeneration: 1, CreationReason: "successor",
	}))
	successor, err := mgr.Launch(ctx, &lifecycle.LaunchRequest{
		TaskID: "restart-child", SessionID: "restart-session", AgentProfileID: "local-stop-profile", WorkspacePath: workspace, ExecutorType: "local",
		DeliveryStreamID: "restart-incarnation:g2", DeliveryIncarnationID: "restart-incarnation", DeliveryHarnessGeneration: 2,
	})
	require.NoError(t, err)
	pm := <-processes
	require.NoError(t, mgr.StartAgentProcess(ctx, successor.ID))
	row, err := repo.GetExecutorRunningBySessionID(ctx, "restart-session")
	require.NoError(t, err)
	session, err := repo.GetTaskSession(ctx, "restart-session")
	require.NoError(t, err)
	result, err := svc.StopTaskForCoordinatorOperation(ctx, "restart-child", "restart-parent", "persisted-restart")
	require.NoError(t, err)
	require.Equal(t, orchestrator.CoordinatorTaskStopStatusStopped, result.Status)
	current, err := repo.GetExecutorRunningBySessionID(ctx, "restart-session")
	require.NoError(t, err)
	require.Equal(t, row, current)
	currentSession, err := repo.GetTaskSession(ctx, "restart-session")
	require.NoError(t, err)
	require.Equal(t, session, currentSession)
	turn, err := repo.GetTurn(ctx, "restart-successor")
	require.NoError(t, err)
	require.Nil(t, turn.CompletedAt)
	require.Equal(t, process.StatusRunning, pm.Status())
}

func newCoordinatorRestartTaskService(repo *taskrepo.Repository, log *logger.Logger) *taskservice.Service {
	svc := taskservice.NewService(taskservice.Repos{
		Workspaces: repo, Tasks: repo, TaskRepos: repo, Workflows: repo, Messages: repo,
		Turns: repo, Sessions: repo, GitSnapshots: repo, RepoEntities: repo, RepositorySets: repo,
		Executors: repo, Environments: repo, TaskEnvironments: repo, Reviews: repo,
		StatusSummaries: repo, WorkspaceFolders: repo,
	}, bus.NewMemoryEventBus(log), log, taskservice.RepositoryDiscoveryConfig{})
	svc.SetWorkspaceBootstrapper(repo)
	svc.SetWorkspacePolicyAttacher(testWorkspacePolicyAttacher{})
	return svc
}

func assertCoordinatorRestartAdmissionCancellation(t *testing.T, ctx context.Context, svc *orchestrator.Service, repo *taskrepo.Repository, pm *process.Manager, request models.CoordinatorStopRequest, candidates []models.CoordinatorStopRequestCandidate, deadline bool) {
	release, err := pm.BeginAdapterOperation()
	require.NoError(t, err)
	defer release()
	ownerCtx, cancel := context.WithCancel(ctx)
	if deadline {
		cancel()
		ownerCtx, cancel = context.WithTimeout(ctx, 500*time.Millisecond)
	}
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- svc.Start(ownerCtx) }()
	require.Eventually(t, func() bool {
		releaseProbe, err := pm.BeginAdapterOperation()
		if err == nil {
			releaseProbe()
		}
		return err != nil
	}, 3*time.Second, time.Millisecond, "actual agentctl admission must close before owner cancellation")
	if !deadline {
		cancel()
	}
	bounded := false
	select {
	case err := <-done:
		require.Error(t, err)
		bounded = true
	case <-time.After(time.Second):
		release()
		<-done
	}
	require.True(t, bounded, "owner cancellation must interrupt the actual replay drain without releasing its admitted operation")
	operations, err := repo.ListCoordinatorStopOperations(ctx, request.TaskID)
	require.NoError(t, err)
	require.Len(t, operations, 1)
	require.NotEqual(t, models.CoordinatorStopOperationStatusStopped, operations[0].Status)
	require.Equal(t, models.CoordinatorStopProofScopePending, operations[0].ProofScope)
	_, saved, _, err := repo.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
	require.NoError(t, err)
	require.Equal(t, candidates, saved)
	release()
}
