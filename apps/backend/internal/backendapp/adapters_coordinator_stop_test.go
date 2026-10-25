package backendapp

import (
	"errors"
	"testing"

	"github.com/kandev/kandev/internal/agent/runtime/lifecycle"
	"github.com/kandev/kandev/internal/orchestrator"
	"github.com/kandev/kandev/internal/orchestrator/executor"
)

func TestLifecycleAdapterForwardsExactCoordinatorStop(t *testing.T) {
	mgr := lifecycle.NewManager(nil, nil, nil, nil, nil, nil, lifecycle.ExecutorFallbackWarn, t.TempDir(), newTestLogger())
	var client executor.AgentManagerClient = newLifecycleAdapter(mgr, nil, newTestLogger())
	stopper, ok := client.(orchestrator.CoordinatorStopGracefulFenceRuntime)
	if !ok {
		t.Fatal("production lifecycle adapter cannot invoke the exact coordinator stop producer")
	}
	err := stopper.StopExecutionWithFence(t.Context(), "missing-exact-execution", 1, "parent stop", func(*lifecycle.ExecutionFenceReceipt) error {
		t.Fatal("unknown execution produced a fence receipt")
		return nil
	}, func() error {
		t.Fatal("unknown execution produced terminal proof")
		return nil
	})
	if !errors.Is(err, lifecycle.ErrExecutionNotFound) {
		t.Fatalf("exact lifecycle producer error = %v, want execution not found", err)
	}
}
