package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentctl "github.com/kandev/kandev/internal/agent/runtime/agentctl"
	"github.com/kandev/kandev/internal/agentctl/server/config"
	"github.com/kandev/kandev/internal/agentctl/server/instance"
	"github.com/kandev/kandev/internal/agentctl/server/process"
	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/pkg/agent"
)

func TestExecutionFenceClosesOnlyMatchingIncarnation(t *testing.T) {
	log := logger.Default()
	mgr := instance.NewManager(&config.Config{
		Ports:    config.PortConfig{Base: 0, Max: 0},
		Defaults: config.InstanceDefaults{Protocol: agent.ProtocolACP},
	}, log)
	t.Cleanup(func() { _ = mgr.Shutdown(context.Background()) })
	mgr.SetServerFactory(func(*config.InstanceConfig, *process.Manager, *logger.Logger) http.Handler {
		return http.NotFoundHandler()
	})
	created, err := mgr.CreateInstance(t.Context(), &instance.CreateRequest{
		ID:                 "instance-fence",
		WorkspacePath:      t.TempDir(),
		ExecutionID:        "execution-fence",
		AgentctlGeneration: 7,
	})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { _ = mgr.StopInstance(context.Background(), created.ID) })

	server := httptest.NewServer(NewControlServer(&config.Config{}, mgr, log).Router())
	t.Cleanup(server.Close)
	host, port := parseHostPort(t, server.URL)
	client := agentctl.NewControlClient(host, port, log)

	_, err = client.CloseExecutionAdmission(t.Context(), created.ID, agentctl.ExecutionFenceRequest{
		ExecutionID: "execution-fence", AgentctlGeneration: 8,
	})
	if err == nil || !strings.Contains(err.Error(), "identity mismatch") {
		t.Fatalf("mismatched generation error = %v, want identity mismatch", err)
	}

	receipt, err := client.CloseExecutionAdmission(t.Context(), created.ID, agentctl.ExecutionFenceRequest{
		ExecutionID: "execution-fence", AgentctlGeneration: 7,
	})
	if err != nil {
		t.Fatalf("CloseExecutionAdmission: %v", err)
	}
	if receipt.ExecutionID != "execution-fence" || receipt.AgentctlGeneration != 7 {
		t.Fatalf("receipt identity = %+v", receipt)
	}
	if receipt.AdmissionClosedAt.IsZero() || !receipt.ManagedProcessesDrained {
		t.Fatalf("receipt = %+v, want closed and drained", receipt)
	}

	_, err = client.CloseExecutionAdmission(t.Context(), created.ID, agentctl.ExecutionFenceRequest{
		ExecutionID: "execution-fence", AgentctlGeneration: 7,
	})
	if err != nil {
		t.Fatalf("idempotent CloseExecutionAdmission: %v", err)
	}
}
