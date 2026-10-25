package instance

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/agentctl/server/config"
	"github.com/kandev/kandev/internal/agentctl/server/process"
	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/pkg/agent"
	"github.com/stretchr/testify/require"
)

// @covers AC-STOP-FENCE-005, AC-STOP-FENCE-008
func TestExecutionFenceRefusesBusyStopOwner(t *testing.T) {
	mgr := NewManager(&config.Config{Ports: config.PortConfig{Base: 0, Max: 0}, Defaults: config.InstanceDefaults{Protocol: agent.ProtocolACP}}, logger.Default())
	t.Cleanup(func() { require.NoError(t, mgr.Shutdown(context.Background())) })
	mgr.SetServerFactory(func(*config.InstanceConfig, *process.Manager, *logger.Logger) http.Handler {
		return http.NotFoundHandler()
	})
	inst, err := mgr.CreateInstance(t.Context(), &CreateRequest{ID: "busy-stop-owner", WorkspacePath: t.TempDir(), ExecutionID: "exact-execution", AgentctlGeneration: 1})
	require.NoError(t, err)
	current, ok := mgr.GetInstance(inst.ID)
	require.True(t, ok)
	current.stopMu.Lock()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := mgr.CloseExecutionAdmission(ctx, inst.ID, ExecutionFenceRequest{ExecutionID: "exact-execution", AgentctlGeneration: 1})
		done <- err
	}()
	bounded := false
	select {
	case err := <-done:
		require.Error(t, err)
		bounded = true
	case <-time.After(200 * time.Millisecond):
	}
	current.stopMu.Unlock()
	if !bounded {
		<-done
	}
	require.True(t, bounded, "canceled fence must not wait for or mutate another stop owner")
	release, err := current.manager.(*process.Manager).BeginAdapterOperation()
	require.NoError(t, err, "busy fence must preserve the other owner admission state")
	release()
	receipt, err := mgr.CloseExecutionAdmission(t.Context(), inst.ID, ExecutionFenceRequest{ExecutionID: "exact-execution", AgentctlGeneration: 1})
	require.NoError(t, err)
	require.True(t, receipt.ManagedProcessesDrained)
}
