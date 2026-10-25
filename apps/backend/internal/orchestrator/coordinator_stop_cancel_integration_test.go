package orchestrator

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

	"github.com/kandev/kandev/internal/agent/agents"
	"github.com/kandev/kandev/internal/agent/registry"
	agentRuntime "github.com/kandev/kandev/internal/agent/runtime"
	agentctl "github.com/kandev/kandev/internal/agent/runtime/agentctl"
	"github.com/kandev/kandev/internal/agent/runtime/lifecycle"
	ctlapi "github.com/kandev/kandev/internal/agentctl/server/api"
	ctlconfig "github.com/kandev/kandev/internal/agentctl/server/config"
	"github.com/kandev/kandev/internal/agentctl/server/instance"
	"github.com/kandev/kandev/internal/agentctl/server/process"
	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/events/bus"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/sqlite"
	"github.com/kandev/kandev/internal/testutil"
	"github.com/kandev/kandev/pkg/agent"
	"github.com/stretchr/testify/require"
)

type cancellationProofProfile struct{}

func (cancellationProofProfile) ResolveProfile(context.Context, string) (*lifecycle.AgentProfileInfo, error) {
	return &lifecycle.AgentProfileInfo{ProfileID: "cancel-profile", AgentName: "mock-agent", Model: "mock-fast"}, nil
}

type cancellationProofManager struct {
	*mockAgentManager
	real *lifecycle.Manager
}

func (m *cancellationProofManager) StopExecutionWithFence(ctx context.Context, id string, generation uint64, reason string, receipt func(*agentRuntime.ExecutionFenceReceipt) error, terminal func() error) error {
	return m.real.StopExecutionWithFence(ctx, id, generation, reason, receipt, terminal)
}

// @covers AC-STOP-FENCE-005, AC-STOP-FENCE-008
func TestCoordinatorStopPeriodicRealAdmissionShutdown(t *testing.T) {
	assertCoordinatorPeriodicRealAdmissionShutdown(t, setupTestRepo(t), false)
}

func TestPostgresCoordinatorStopPeriodicRealAdmissionShutdown(t *testing.T) {
	db := testutil.OpenIsolatedPostgres(t, testutil.PostgresDSNFromEnv(t))
	repo, err := sqlite.NewWithDB(db, db, nil)
	require.NoError(t, err)
	assertCoordinatorPeriodicRealAdmissionShutdown(t, repo, false)
}

func TestCoordinatorStopPeriodicRealSessionGuardShutdown(t *testing.T) {
	assertCoordinatorPeriodicRealAdmissionShutdown(t, setupTestRepo(t), true)
}

func TestPostgresCoordinatorStopPeriodicRealSessionGuardShutdown(t *testing.T) {
	db := openCoordinatorGuardPostgres(t)
	repo, err := sqlite.NewWithDB(db, db, nil)
	require.NoError(t, err)
	assertCoordinatorPeriodicRealAdmissionShutdown(t, repo, true)
}

func assertCoordinatorPeriodicRealAdmissionShutdown(t *testing.T, repo *sqlite.Repository, competingCaller bool) {
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	_, prior, _ := newLegacyStopFixtureWithRepo(t, repo)
	log := testLogger()
	binary := filepath.Join(t.TempDir(), "mock-agent")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/mock-agent")
	build.Dir = "../.."
	output, err := build.CombinedOutput()
	require.NoError(t, err, "%s", output)
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := probe.Addr().(*net.TCPAddr).Port
	require.NoError(t, probe.Close())
	control := instance.NewManager(&ctlconfig.Config{Ports: ctlconfig.PortConfig{Base: port, Max: port}, Defaults: ctlconfig.InstanceDefaults{Protocol: agent.ProtocolACP}}, log)
	processes := make(chan *process.Manager, 1)
	control.SetServerFactory(func(cfg *ctlconfig.InstanceConfig, pm *process.Manager, log *logger.Logger) http.Handler {
		processes <- pm
		router := ctlapi.NewServer(cfg, pm, nil, nil, log).Router()
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// This fixture owns agent/admission work; no workspace-stream subscription is needed.
			if strings.HasPrefix(r.URL.Path, "/api/v1/workspace/stream") {
				http.NotFound(w, r)
				return
			}
			router.ServeHTTP(w, r)
		})
	})
	t.Cleanup(func() { require.NoError(t, control.Shutdown(context.Background())) })
	httpControl := httptest.NewServer(ctlapi.NewControlServer(&ctlconfig.Config{}, control, log).Router())
	t.Cleanup(httpControl.Close)
	host, portText, err := net.SplitHostPort(strings.TrimPrefix(httpControl.URL, "http://"))
	require.NoError(t, err)
	controlPort, err := strconv.Atoi(portText)
	require.NoError(t, err)
	backends := lifecycle.NewExecutorRegistry(log)
	backends.Register(lifecycle.NewStandaloneExecutor(agentctl.NewControlClient(host, controlPort, log), host, controlPort, log))
	reg := registry.NewRegistry(log)
	mock := agents.NewMockAgent()
	mock.SetEnabled(true)
	mock.SetBinaryPath(binary)
	mock.SetSupportsMCP(false)
	require.NoError(t, reg.Register(mock))
	real := lifecycle.NewManager(reg, bus.NewMemoryEventBus(log), backends, nil, cancellationProofProfile{}, nil, lifecycle.ExecutorFallbackWarn, t.TempDir(), log)
	real.SetExecutorRunningWriter(repo)
	t.Cleanup(func() { require.NoError(t, real.Stop()) })
	const deliveryIncarnation = "legacy-cancel-incarnation"
	require.NoError(t, repo.CreateHarnessSessionGeneration(ctx, &models.HarnessSessionGeneration{
		SessionID: "legacy-session", IncarnationID: deliveryIncarnation, Generation: 1, CreationReason: "initial",
	}))
	execution, err := real.Launch(ctx, &lifecycle.LaunchRequest{
		TaskID: "legacy-task", SessionID: "legacy-session", AgentProfileID: "cancel-profile", WorkspacePath: t.TempDir(), ExecutorType: "local",
		DeliveryStreamID: "legacy-session", DeliveryIncarnationID: deliveryIncarnation, DeliveryHarnessGeneration: 1,
	})
	require.NoError(t, err)
	pm := <-processes
	require.NoError(t, real.StartAgentProcess(ctx, execution.ID))
	running, err := repo.GetExecutorRunningBySessionID(ctx, "legacy-session")
	require.NoError(t, err)
	request := models.CoordinatorStopRequest{TaskID: "legacy-task", ParentTaskID: "legacy-parent", OperationID: "real-periodic-cancel"}
	_, captured, _, err := repo.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
	require.NoError(t, err)
	require.NoError(t, repo.DeleteExecutorRunningBySessionID(ctx, "legacy-session"))
	manager := &cancellationProofManager{mockAgentManager: prior.agentManager.(*mockAgentManager), real: real}
	observed := &coordinatorGuardObservedRepo{Repository: repo, entered: make(chan struct{}, 1)}
	newService := func() *Service {
		svc := NewService(DefaultServiceConfig(), bus.NewMemoryEventBus(log), manager, prior.taskRepo, observed, nil, nil, nil, log)
		svc.SetTurnService(&repoTurnService{repo: repo})
		return svc
	}
	svc := newService()
	ticks := make(chan time.Time, 1)
	svc.coordinatorStopReconciler.ticks = ticks
	require.NoError(t, svc.Start(ctx))
	t.Cleanup(func() {
		if svc.running {
			require.NoError(t, svc.Stop())
		}
	})
	release, err := pm.BeginAdapterOperation()
	require.NoError(t, err)
	defer release()
	require.NoError(t, repo.UpsertExecutorRunning(ctx, running))
	var parentStopDone chan error
	if competingCaller {
		parentStopDone = make(chan error, 1)
		go func() {
			_, err := svc.StopTaskForCoordinatorOperation(ctx, request.TaskID, request.ParentTaskID, request.OperationID)
			parentStopDone <- err
		}()
	} else {
		ticks <- time.Now()
	}
	require.Eventually(t, func() bool {
		probeRelease, err := pm.BeginAdapterOperation()
		if err == nil {
			probeRelease()
		}
		return err != nil
	}, 3*time.Second, time.Millisecond, "periodic production replay must close real admission")
	if competingCaller {
		observed.observe.Store(true)
		ticks <- time.Now()
		select {
		case <-observed.entered:
		case <-time.After(3 * time.Second):
			release()
			<-parentStopDone
			require.NoError(t, svc.Stop())
			t.Fatal("periodic replay did not revisit the captured candidate")
		}
	}
	done := make(chan error, 1)
	go func() { done <- svc.Stop() }()
	joined := false
	select {
	case err := <-done:
		require.NoError(t, err)
		joined = true
	case <-time.After(time.Second):
		release()
		require.NoError(t, <-done)
	}
	require.True(t, joined, "Service.Stop must join replay while the real admitted operation remains held")
	require.Equal(t, process.StatusRunning, pm.Status())
	drainCtx, stopDrain := context.WithTimeout(ctx, 10*time.Millisecond)
	defer stopDrain()
	require.ErrorIs(t, pm.WaitForAdmission(drainCtx), context.DeadlineExceeded)
	operations, err := repo.ListCoordinatorStopOperations(ctx, request.TaskID)
	require.NoError(t, err)
	require.Len(t, operations, 1)
	require.NotEqual(t, models.CoordinatorStopOperationStatusStopped, operations[0].Status)
	require.Equal(t, models.CoordinatorStopProofScopePending, operations[0].ProofScope)
	_, saved, _, err := repo.CaptureCoordinatorStopRequestWithCandidates(ctx, request)
	require.NoError(t, err)
	require.Equal(t, captured, saved)
	release()
	if competingCaller {
		require.NoError(t, <-parentStopDone)
	}
	observed.observe.Store(false)
	retry := newService()
	require.NoError(t, retry.Start(ctx))
	t.Cleanup(func() { require.NoError(t, retry.Stop()) })
	require.Eventually(t, func() bool {
		result, err := retry.StopTaskForCoordinatorOperation(ctx, request.TaskID, request.ParentTaskID, request.OperationID)
		return err == nil && result.Status == CoordinatorTaskStopStatusStopped
	}, 3*time.Second, time.Millisecond, "new service must resume the same durable canceled request after owned drain")
	require.Equal(t, process.StatusStopped, pm.Status())
}
