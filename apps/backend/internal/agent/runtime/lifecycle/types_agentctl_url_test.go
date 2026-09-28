package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"testing"

	agentctl "github.com/kandev/kandev/internal/agent/runtime/agentctl"
	"github.com/kandev/kandev/internal/common/logger"
)

type generationControllerFunc func(context.Context, string, agentctl.ExecutionGenerationUpdate) (*agentctl.InstanceInfo, error)

func (f generationControllerFunc) UpdateExecutionGeneration(ctx context.Context, instanceID string, update agentctl.ExecutionGenerationUpdate) (*agentctl.InstanceInfo, error) {
	return f(ctx, instanceID, update)
}

func newNopLogger(t *testing.T) *logger.Logger {
	t.Helper()
	log, err := logger.NewLogger(logger.LoggingConfig{Level: "error", Format: "json"})
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	return log
}

func TestAgentExecution_AgentctlURL_NilClient(t *testing.T) {
	t.Parallel()
	exec := &AgentExecution{ID: "exec-1"}
	if got := exec.AgentctlURL(); got != "" {
		t.Errorf("expected empty string when no client set, got %q", got)
	}
}

func TestAgentExecution_AgentctlURL_WithClient(t *testing.T) {
	t.Parallel()
	log := newNopLogger(t)
	client := agentctl.NewClient("127.0.0.1", 12345, log)
	exec := &AgentExecution{
		ID:       "exec-2",
		agentctl: client,
	}
	want := fmt.Sprintf("http://%s:%d", "127.0.0.1", 12345)
	if got := exec.AgentctlURL(); got != want {
		t.Errorf("AgentctlURL() = %q, want %q", got, want)
	}
}

func TestAgentExecution_AcquireAgentCtlClientPinsReplacement(t *testing.T) {
	t.Parallel()
	client := agentctl.NewClient("127.0.0.1", 12345, newNopLogger(t))
	exec := &AgentExecution{ID: "exec-lease", agentctl: client}

	acquired, release := exec.AcquireAgentCtlClient()
	if acquired != client {
		t.Fatalf("AcquireAgentCtlClient() = %p, want %p", acquired, client)
	}
	if exec.agentctlLifecycleMu.TryLock() {
		exec.agentctlLifecycleMu.Unlock()
		release()
		t.Fatal("replacement lock acquired while client lease was active")
	}

	release()
	if !exec.agentctlLifecycleMu.TryLock() {
		t.Fatal("replacement lock remained blocked after client lease release")
	}
	exec.agentctlLifecycleMu.Unlock()
}

func TestAgentExecutionAdvanceAgentctlGenerationUsesExactPreviousGeneration(t *testing.T) {
	var gotID string
	var gotUpdate agentctl.ExecutionGenerationUpdate
	exec := &AgentExecution{ID: "execution-generation", agentctlControl: generationControllerFunc(
		func(_ context.Context, instanceID string, update agentctl.ExecutionGenerationUpdate) (*agentctl.InstanceInfo, error) {
			gotID, gotUpdate = instanceID, update
			return &agentctl.InstanceInfo{ExecutionID: instanceID, AgentctlGeneration: update.AgentctlGeneration}, nil
		},
	)}
	if err := exec.advanceAgentctlGeneration(t.Context(), 2); err != nil {
		t.Fatalf("advanceAgentctlGeneration: %v", err)
	}
	if gotID != "execution-generation" || gotUpdate.ExecutionID != gotID ||
		gotUpdate.ExpectedAgentctlGeneration != 1 || gotUpdate.AgentctlGeneration != 2 {
		t.Fatalf("generation update = (%q, %+v), want exact 1 -> 2", gotID, gotUpdate)
	}
}

func TestAgentExecutionAdvanceAgentctlGenerationRejectsControlFailure(t *testing.T) {
	exec := &AgentExecution{ID: "execution-generation", agentctlControl: generationControllerFunc(
		func(context.Context, string, agentctl.ExecutionGenerationUpdate) (*agentctl.InstanceInfo, error) {
			return nil, errors.New("identity mismatch")
		},
	)}
	if err := exec.advanceAgentctlGeneration(t.Context(), 2); err == nil {
		t.Fatal("advanceAgentctlGeneration succeeded after control CAS failure")
	}
}
