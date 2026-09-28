package instance

import (
	"context"
	"errors"
	"net/http"
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
