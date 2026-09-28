package instance

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

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
	if _, err := procMgr.StartProcess(t.Context(), process.StartProcessRequest{
		SessionID: "session-fence-live", Command: "true",
	}); !errors.Is(err, process.ErrManagerStopping) {
		t.Fatalf("StartProcess after fence error = %v, want ErrManagerStopping", err)
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
