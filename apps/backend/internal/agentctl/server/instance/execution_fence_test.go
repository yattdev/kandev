package instance

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/agentctl/server/config"
	"github.com/kandev/kandev/internal/agentctl/server/process"
	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/pkg/agent"
)

func TestCloseExecutionAdmissionKeepsLiveManagedProcessIncomplete(t *testing.T) {
	mgr := NewManager(&config.Config{
		Ports:    config.PortConfig{Base: 0, Max: 0},
		Defaults: config.InstanceDefaults{Protocol: agent.ProtocolACP},
	}, logger.Default())
	t.Cleanup(func() { _ = mgr.Shutdown(context.Background()) })
	mgr.SetServerFactory(func(*config.InstanceConfig, *process.Manager, *logger.Logger) http.Handler {
		return http.NotFoundHandler()
	})
	created, err := mgr.CreateInstance(t.Context(), &CreateRequest{
		ID:                 "instance-fence-live",
		WorkspacePath:      t.TempDir(),
		ExecutionID:        "execution-fence-live",
		AgentctlGeneration: 3,
	})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { _ = mgr.StopInstance(context.Background(), created.ID) })

	inst, ok := mgr.GetInstance(created.ID)
	if !ok {
		t.Fatal("created instance missing")
	}
	procMgr := inst.manager.(*process.Manager)
	if _, err := procMgr.StartProcess(t.Context(), process.StartProcessRequest{
		SessionID: "session-fence-live", Command: "sleep 30",
	}); err != nil {
		t.Fatalf("StartProcess: %v", err)
	}

	receipt, err := mgr.CloseExecutionAdmission(t.Context(), created.ID, ExecutionFenceRequest{
		ExecutionID: "execution-fence-live", AgentctlGeneration: 3,
	})
	if err != nil {
		t.Fatalf("CloseExecutionAdmission: %v", err)
	}
	if receipt.ManagedProcessesDrained || receipt.ManagedProcessCount != 1 {
		t.Fatalf("receipt = %+v, want one live managed process and incomplete drain", receipt)
	}
	if inst.fenceBoundary == nil || !inst.fenceBoundary.Cutoff.Known || inst.fenceBoundary.Cutoff.Sequence == 0 ||
		inst.fenceBoundary.ExecutionID != "execution-fence-live" || inst.fenceBoundary.AgentctlGeneration != 3 {
		t.Fatalf("internal fence boundary = %+v, want exact execution generation and known sequence", inst.fenceBoundary)
	}
	original := *inst.fenceBoundary
	if _, err := mgr.CloseExecutionAdmission(t.Context(), created.ID, ExecutionFenceRequest{
		ExecutionID: "execution-fence-live", AgentctlGeneration: 3,
	}); err != nil {
		t.Fatalf("replay exact fence: %v", err)
	}
	if *inst.fenceBoundary != original {
		t.Fatalf("replayed boundary = %+v, want immutable %+v", inst.fenceBoundary, original)
	}
	if _, err := mgr.UpdateExecutionGeneration(t.Context(), created.ID, ExecutionGenerationUpdate{
		ExecutionID: "execution-fence-live", ExpectedAgentctlGeneration: 3, AgentctlGeneration: 4,
	}); !errors.Is(err, ErrExecutionIdentityMismatch) {
		t.Fatalf("generation update after fence = %v, want identity mismatch", err)
	}
	if got := len(procMgr.ListProcesses("session-fence-live")); got != 1 {
		t.Fatalf("managed process count after fence = %d, want the admitted command to remain live", got)
	}
	if _, err := procMgr.StartProcess(t.Context(), process.StartProcessRequest{
		SessionID: "session-fence-live", Command: "true",
	}); !errors.Is(err, process.ErrManagerStopping) {
		t.Fatalf("StartProcess after fence error = %v, want ErrManagerStopping", err)
	}
}

type legacyFenceManager struct {
	*fakeProcessManager
	closed bool
}

func (m *legacyFenceManager) CloseAdmissionGracefully() time.Time {
	m.closed = true
	return time.Now().UTC()
}
func (m *legacyFenceManager) WaitForAdmission(context.Context) error     { return nil }
func (m *legacyFenceManager) ListProcesses(string) []process.ProcessInfo { return nil }
func (m *legacyFenceManager) Status() process.Status                     { return process.StatusStopped }

func TestLegacyFenceSequenceIsUnknown(t *testing.T) {
	mgr := NewManager(&config.Config{}, logger.Default())
	legacy := &legacyFenceManager{fakeProcessManager: &fakeProcessManager{}}
	mgr.instances["legacy-instance"] = &Instance{
		ID: "legacy-instance", ExecutionID: "legacy-execution", AgentctlGeneration: 1, manager: legacy,
	}
	_, err := mgr.CloseExecutionAdmission(t.Context(), "legacy-instance", ExecutionFenceRequest{
		ExecutionID: "legacy-execution", AgentctlGeneration: 1,
	})
	if !errors.Is(err, ErrAdmissionSequenceUnknown) || !legacy.closed {
		t.Fatalf("legacy fence error = %v, closed = %t; want unknown after closure", err, legacy.closed)
	}
	if mgr.instances["legacy-instance"].fenceBoundary != nil {
		t.Fatal("legacy fence invented a known boundary")
	}
}

func TestFenceRefusesStaleGenerationAfterInstanceAdvance(t *testing.T) {
	mgr := NewManager(&config.Config{
		Ports: config.PortConfig{Base: 0, Max: 0}, Defaults: config.InstanceDefaults{Protocol: agent.ProtocolACP},
	}, logger.Default())
	t.Cleanup(func() { _ = mgr.Shutdown(context.Background()) })
	mgr.SetServerFactory(func(*config.InstanceConfig, *process.Manager, *logger.Logger) http.Handler {
		return http.NotFoundHandler()
	})
	created, err := mgr.CreateInstance(t.Context(), &CreateRequest{
		ID: "instance-generation-replay", WorkspacePath: t.TempDir(),
		ExecutionID: "execution-generation-replay", AgentctlGeneration: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mgr.StopInstance(context.Background(), created.ID) })
	if _, err := mgr.UpdateExecutionGeneration(t.Context(), created.ID, ExecutionGenerationUpdate{
		ExecutionID: "execution-generation-replay", ExpectedAgentctlGeneration: 1, AgentctlGeneration: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.CloseExecutionAdmission(t.Context(), created.ID, ExecutionFenceRequest{
		ExecutionID: "execution-generation-replay", AgentctlGeneration: 1,
	}); !errors.Is(err, ErrExecutionIdentityMismatch) {
		t.Fatalf("stale fence = %v, want identity mismatch", err)
	}
	inst, _ := mgr.GetInstance(created.ID)
	if inst.fenceBoundary != nil {
		t.Fatal("stale fence closed current generation")
	}
	if _, err := mgr.CloseExecutionAdmission(t.Context(), created.ID, ExecutionFenceRequest{
		ExecutionID: "execution-generation-replay", AgentctlGeneration: 2,
	}); err != nil {
		t.Fatalf("current fence: %v", err)
	}
	if inst.fenceBoundary == nil || inst.fenceBoundary.AgentctlGeneration != 2 {
		t.Fatalf("current boundary = %+v, want generation 2", inst.fenceBoundary)
	}
}

func TestUpdateExecutionGenerationAllowsOnlyOneConcurrentCAS(t *testing.T) {
	mgr := NewManager(&config.Config{
		Ports:    config.PortConfig{Base: 0, Max: 0},
		Defaults: config.InstanceDefaults{Protocol: agent.ProtocolACP},
	}, logger.Default())
	t.Cleanup(func() { _ = mgr.Shutdown(context.Background()) })
	mgr.SetServerFactory(func(*config.InstanceConfig, *process.Manager, *logger.Logger) http.Handler {
		return http.NotFoundHandler()
	})
	created, err := mgr.CreateInstance(t.Context(), &CreateRequest{
		ID: "instance-generation-race", WorkspacePath: t.TempDir(), ExecutionID: "execution-generation-race", AgentctlGeneration: 1,
	})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { _ = mgr.StopInstance(context.Background(), created.ID) })

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := mgr.UpdateExecutionGeneration(t.Context(), created.ID, ExecutionGenerationUpdate{
				ExecutionID: "execution-generation-race", ExpectedAgentctlGeneration: 1, AgentctlGeneration: 2,
			})
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	var successes, mismatches int
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrExecutionIdentityMismatch):
			mismatches++
		default:
			t.Fatalf("UpdateExecutionGeneration error = %v", err)
		}
	}
	if successes != 1 || mismatches != 1 {
		t.Fatalf("CAS results = %d success, %d mismatch; want one of each", successes, mismatches)
	}
	info, ok := mgr.GetInstance(created.ID)
	if !ok || info.Info().AgentctlGeneration != 2 {
		t.Fatalf("generation after race = %+v, want 2", info)
	}
}
