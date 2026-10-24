package instance

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/kandev/kandev/internal/agentctl/server/config"
	"github.com/kandev/kandev/internal/agentctl/server/process"
	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/pkg/agent"
)

// TestCreateInstance_RecordsCreateReadyMillis pins the diagnostic
// agentctl_create_ready_ms metric's write side: CreateInstance must stamp a
// positive elapsed-milliseconds value onto the InstanceConfig it builds, so
// api.handleSystemMetrics has something other than the "not yet recorded"
// zero sentinel to report for a real instance.
func TestCreateInstance_RecordsCreateReadyMillis(t *testing.T) {
	log := newTestLogger(t)
	mgr := NewManager(&config.Config{
		Ports:    config.PortConfig{Base: 0, Max: 0},
		Defaults: config.InstanceDefaults{Protocol: agent.ProtocolACP},
	}, log)
	t.Cleanup(func() { _ = mgr.Shutdown(context.Background()) })

	var captured *config.InstanceConfig
	mgr.SetServerFactory(func(cfg *config.InstanceConfig, procMgr *process.Manager, log *logger.Logger) http.Handler {
		captured = cfg
		return http.NotFoundHandler()
	})

	resp, err := mgr.CreateInstance(context.Background(), &CreateRequest{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { _ = mgr.StopInstance(context.Background(), resp.ID) })

	if captured == nil {
		t.Fatal("server factory was never invoked; no InstanceConfig captured")
	}
	if captured.CreateReadyMillis == nil {
		t.Fatal("CreateReadyMillis is nil on a config built by NewInstanceConfig")
	}
	if millis := captured.CreateReadyMillis.Load(); millis <= 0 {
		t.Errorf("CreateReadyMillis = %d, want > 0 after a completed creation", millis)
	}
}

func TestCreateInstance_PropagatesMCPToolNamePresentationCapability(t *testing.T) {
	log := newTestLogger(t)
	mgr := NewManager(&config.Config{
		Ports:    config.PortConfig{Base: 0, Max: 0},
		Defaults: config.InstanceDefaults{Protocol: agent.ProtocolACP},
	}, log)
	t.Cleanup(func() { _ = mgr.Shutdown(context.Background()) })

	var captured *config.InstanceConfig
	mgr.SetServerFactory(func(cfg *config.InstanceConfig, procMgr *process.Manager, log *logger.Logger) http.Handler {
		captured = cfg
		return http.NotFoundHandler()
	})

	resp, err := mgr.CreateInstance(context.Background(), &CreateRequest{
		WorkspacePath:              t.TempDir(),
		NamespacesMCPToolsByServer: true,
	})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { _ = mgr.StopInstance(context.Background(), resp.ID) })

	if captured == nil || !captured.NamespacesMCPToolsByServer {
		t.Fatalf("captured instance config = %+v, want namespaced MCP tools", captured)
	}
}

func TestCreateInstanceCarriesValidatedWorkspaceIsolationIntoConfig(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("object pinning requires Linux")
	}
	log := newTestLogger(t)
	mgr := NewManager(&config.Config{Ports: config.PortConfig{Base: 0, Max: 0}}, log)
	t.Cleanup(func() { _ = mgr.Shutdown(context.Background()) })
	var captured *config.InstanceConfig
	mgr.SetServerFactory(func(cfg *config.InstanceConfig, _ *process.Manager, _ *logger.Logger) http.Handler {
		captured = cfg
		return http.NotFoundHandler()
	})
	root := t.TempDir()
	shared := filepath.Join(root, "repo", ".git")
	private := filepath.Join(shared, "worktrees", "task")
	workspace := filepath.Join(root, "task")
	for _, path := range []string{private, workspace} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	isolation := &config.WorkspaceIsolation{
		SharedCommonDirs:       []string{shared},
		WritablePaths:          []string{private},
		PrivateAdminPaths:      []string{private},
		WritableWorkspaceRoots: []string{workspace},
	}
	if err := isolation.CaptureObjects(); err != nil {
		t.Fatal(err)
	}
	resp, err := mgr.CreateInstance(context.Background(), &CreateRequest{
		WorkspacePath: workspace, WorkspaceIsolation: isolation,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mgr.StopInstance(context.Background(), resp.ID) })
	if captured == nil || captured.WorkspaceIsolation == nil ||
		len(captured.WorkspaceIsolation.PrivateAdminPaths) != 1 ||
		captured.WorkspaceIsolation.PrivateAdminPaths[0] != isolation.PrivateAdminPaths[0] {
		t.Fatalf("instance config lost isolation manifest: %+v", captured)
	}
	isolation.PrivateAdminPaths[0] = filepath.Join(shared, "refs")
	if captured.WorkspaceIsolation.PrivateAdminPaths[0] == isolation.PrivateAdminPaths[0] {
		t.Fatal("instance config retained mutable request isolation paths")
	}
	isolation.WritableWorkspaceRoots[0] = filepath.Join(root, "changed")
	if captured.WorkspaceIsolation.WritableWorkspaceRoots[0] == isolation.WritableWorkspaceRoots[0] {
		t.Fatal("instance config retained mutable workspace roots")
	}
	isolation.ObjectIdentities[0].Path = filepath.Join(root, "forged")
	if captured.WorkspaceIsolation.ObjectIdentities[0].Path == isolation.ObjectIdentities[0].Path {
		t.Fatal("instance config retained mutable object identities")
	}
	if _, err := mgr.CreateInstance(context.Background(), &CreateRequest{
		WorkspacePath: t.TempDir(), WorkspaceIsolation: isolation,
	}); err == nil {
		t.Fatal("create instance accepted a forged private administration path")
	}
	isolation.PrivateAdminPaths[0] = private
	isolation.WritablePaths = []string{"/tmp/invalid\x00path"}
	if _, err := mgr.CreateInstance(context.Background(), &CreateRequest{
		WorkspacePath: t.TempDir(), WorkspaceIsolation: isolation,
	}); err == nil {
		t.Fatal("create instance accepted a NUL path in its isolation manifest")
	}
}
