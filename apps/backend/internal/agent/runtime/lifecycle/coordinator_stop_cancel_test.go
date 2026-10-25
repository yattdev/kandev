package lifecycle

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// @covers AC-STOP-FENCE-005, AC-STOP-FENCE-008
func TestStopExecutionWithFenceRefusesBusyReplacementLock(t *testing.T) {
	mgr := newTestManager(t)
	execution := &AgentExecution{ID: "busy-exact-stop", startupAttemptGeneration: 1}
	require.NoError(t, mgr.executionStore.Add(execution))
	execution.remoteInstanceLifecycleMu.Lock()
	ownerCtx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- mgr.StopExecutionWithFence(ownerCtx, execution.ID, 1, "owned stop", nil, nil) }()
	bounded := false
	select {
	case err := <-done:
		require.Error(t, err)
		bounded = true
	case <-time.After(200 * time.Millisecond):
	}
	execution.remoteInstanceLifecycleMu.Unlock()
	if !bounded {
		<-done
	}
	require.True(t, bounded, "exact replay must not wait indefinitely for a replacement owner")
	_, retained := mgr.executionStore.Get(execution.ID)
	require.True(t, retained)
}

func TestAgentExecutionExactStopRefusesBusyAgentctlLock(t *testing.T) {
	for _, mode := range []string{"fence", "graceful stop"} {
		t.Run(mode, func(t *testing.T) {
			execution := &AgentExecution{ID: "busy-agentctl-stop", startupAttemptGeneration: 1}
			execution.agentctlLifecycleMu.Lock()
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				if mode == "fence" {
					_, err := execution.CloseExecutionAdmission(ctx, 1)
					done <- err
				} else {
					done <- execution.StopAgentctlGracefully(ctx, 1)
				}
			}()
			bounded := false
			select {
			case err := <-done:
				require.Error(t, err)
				bounded = true
			case <-time.After(200 * time.Millisecond):
			}
			execution.agentctlLifecycleMu.Unlock()
			if !bounded {
				<-done
			}
			require.True(t, bounded, "exact runtime operation must refuse another client lifecycle owner")
		})
	}
}

func TestAgentExecutionExactFenceRefusesBusyGenerationOwner(t *testing.T) {
	execution := &AgentExecution{ID: "busy-generation-owner", startupAttemptGeneration: 1}
	execution.startupLifecycleMu.Lock()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := execution.CloseExecutionAdmission(ctx, 1); done <- err }()
	bounded := false
	select {
	case err := <-done:
		require.Error(t, err)
		bounded = true
	case <-time.After(200 * time.Millisecond):
	}
	execution.startupLifecycleMu.Unlock()
	if !bounded {
		<-done
	}
	require.True(t, bounded, "exact fence must not wait on another generation owner")
}
