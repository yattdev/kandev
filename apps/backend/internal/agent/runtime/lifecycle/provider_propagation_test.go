package lifecycle

import (
	"context"
	"reflect"
	"testing"

	"github.com/kandev/kandev/internal/agent/agents"
	agentctl "github.com/kandev/kandev/internal/agent/runtime/agentctl"
	"github.com/kandev/kandev/internal/task/models"
)

func TestAgentctlProviderMappingsPreserveProviderCapabilities(t *testing.T) {
	providersByCase := map[string][]string{
		"github-only":          {"github"},
		"gitlab-only":          {"gitlab"},
		"mixed":                {"github", "gitlab"},
		"empty":                {},
		"unsupported-filtered": {},
	}

	for name, providers := range providersByCase {
		providers := providers
		t.Run(name, func(t *testing.T) {
			req := &ExecutorCreateRequest{McpProviders: providers}
			standalone := buildStandaloneCreateInstanceRequest(req, nil, "", false, false, false, false, nil)
			container := buildContainerCreateInstanceRequest(ContainerConfig{McpProviders: providers}, "", false, false, false, false, nil)
			docker := buildReconnectCreateInstanceRequest(req, "previous-execution")
			sprites := spriteCreateInstanceRequest(req)
			ssh := buildSSHCreateInstanceRequest(req, "/workspace", "/remote/agentctl")

			mapped := map[string][]string{
				"standalone":    standalone.McpProviders,
				"container":     container.McpProviders,
				"docker":        docker.McpProviders,
				"sprites":       sprites.McpProviders,
				executorTypeSSH: ssh.McpProviders,
			}
			for backend, got := range mapped {
				if !reflect.DeepEqual(got, providers) {
					t.Errorf("%s provider mapping = %#v, want %#v", backend, got, providers)
				}
			}
		})
	}
}

func TestAgentctlMCPToolNamePresentationCapabilityPropagatesThroughExecutors(t *testing.T) {
	agent := &fakeRuntimeAgent{
		MockAgent:          agents.NewMockAgent(),
		id:                 "auggie",
		namespacesMCPTools: true,
	}
	req := &ExecutorCreateRequest{AgentConfig: agent}
	sprites := spriteCreateInstanceRequest(req)
	ssh := buildSSHCreateInstanceRequest(req, "/workspace", "/remote/agentctl")

	requests := map[string]*agentctl.CreateInstanceRequest{
		"standalone": buildStandaloneCreateInstanceRequest(req, nil, "", false, false, false, false, nil),
		"container":  buildContainerCreateInstanceRequest(ContainerConfig{AgentConfig: agent}, "", false, false, false, false, nil),
		"docker":     buildReconnectCreateInstanceRequest(req, "previous-execution"),
		"sprites":    &sprites,
		"ssh":        &ssh,
	}
	for executor, request := range requests {
		if !request.NamespacesMCPToolsByServer {
			t.Errorf("%s request lost NamespacesMCPToolsByServer", executor)
		}
	}
}

func TestManagerLaunchCarriesMCPProvidersToExecutor(t *testing.T) {
	mgr, backend := newEnvironmentExecutionTestManager(t, nil)
	want := []string{"github", "gitlab"}
	_, err := mgr.Launch(context.Background(), &LaunchRequest{
		TaskID:         "task-provider-launch",
		SessionID:      "session-provider-launch",
		AgentProfileID: "profile-provider-launch",
		ExecutorType:   string(models.ExecutorTypeLocal),
		IsEphemeral:    true,
		McpProviders:   want,
	})
	if err != nil {
		t.Fatalf("Manager.Launch: %v", err)
	}
	if backend.lastRequest == nil {
		t.Fatal("executor CreateInstance was not called")
	}
	if !reflect.DeepEqual(backend.lastRequest.McpProviders, want) {
		t.Fatalf("executor McpProviders = %#v, want %#v", backend.lastRequest.McpProviders, want)
	}
	if backend.lastRequest.AgentctlGeneration != initialAgentctlGeneration {
		t.Fatalf("executor AgentctlGeneration = %d, want %d", backend.lastRequest.AgentctlGeneration, initialAgentctlGeneration)
	}
}

func TestAgentctlInstanceRequestsCarryInitialExecutionGeneration(t *testing.T) {
	req := &ExecutorCreateRequest{InstanceID: "execution-1", AgentctlGeneration: initialAgentctlGeneration}
	sprites := spriteCreateInstanceRequest(req)
	ssh := buildSSHCreateInstanceRequest(req, "/workspace", "/agentctl")
	requests := map[string]*agentctl.CreateInstanceRequest{
		"standalone": buildStandaloneCreateInstanceRequest(req, nil, "", false, false, false, false, nil),
		"container":  buildContainerCreateInstanceRequest(ContainerConfig{InstanceID: req.InstanceID, AgentctlGeneration: req.AgentctlGeneration}, "", false, false, false, false, nil),
		"ssh":        &ssh,
		"sprites":    &sprites,
		"reconnect":  buildReconnectCreateInstanceRequest(req, req.InstanceID),
	}
	for name, create := range requests {
		if create.ExecutionID != req.InstanceID || create.AgentctlGeneration != initialAgentctlGeneration {
			t.Errorf("%s identity = (%q, %d), want (%q, %d)", name, create.ExecutionID, create.AgentctlGeneration, req.InstanceID, initialAgentctlGeneration)
		}
	}
}
