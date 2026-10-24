//go:build linux

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/kandev/kandev/internal/agentctl/isolationbroker"
	"github.com/kandev/kandev/internal/agentctl/server/config"
	"github.com/kandev/kandev/internal/agentctl/server/instance"
	"github.com/kandev/kandev/internal/common/logger"
)

func TestProtectedCreateConsumesExactBrokerReceipt(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "repo", ".git")
	private := filepath.Join(shared, "worktrees", "task")
	workspace := filepath.Join(root, "task")
	for _, path := range []string{private, workspace} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	manifest := &config.WorkspaceIsolation{
		SharedCommonDirs: []string{shared}, WritablePaths: []string{private},
		PrivateAdminPaths: []string{private}, WritableWorkspaceRoots: []string{workspace},
	}
	pins, err := manifest.CaptureObjectsPinned()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pins.Close() }()
	parent, child, err := isolationbroker.SocketPair()
	if err != nil {
		t.Fatal(err)
	}
	mgr := instance.NewManager(&config.Config{Ports: config.PortConfig{Base: 0, Max: 0}}, logger.Default())
	t.Cleanup(func() { _ = mgr.Shutdown(context.Background()) })
	server := NewControlServer(&config.Config{AuthToken: "control-token"}, mgr, logger.Default())
	broker, err := isolationbroker.NewServer(child, server.CredentialSource().Invalidated)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()
	server.SetIsolationBroker(broker)
	client, err := isolationbroker.NewClient(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	req := instance.CreateRequest{ID: "instance", TaskID: "task", SessionID: "session", WorkspacePath: workspace, WorkspaceIsolation: manifest}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	reg := isolationbroker.Registration{TaskID: req.TaskID, SessionID: req.SessionID, InstanceID: req.ID, Workspace: workspace, Identities: manifest.ObjectIdentities}
	receipt, err := client.Register(context.Background(), body, reg, pins)
	if err != nil {
		t.Fatal(err)
	}
	send := func(body []byte, receipt string) *httptest.ResponseRecorder {
		httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/instances", bytes.NewReader(body))
		httpReq.Header.Set("Authorization", "Bearer control-token")
		httpReq.Header.Set("X-Kandev-Isolation-Receipt", receipt)
		response := httptest.NewRecorder()
		server.Router().ServeHTTP(response, httpReq)
		return response
	}
	if got := send(body, receipt); got.Code != http.StatusCreated {
		t.Fatalf("registered protected create = %d %s", got.Code, got.Body.String())
	}
	statusReq := httptest.NewRequest(http.MethodGet, "/api/v1/isolation/receipts/"+receipt, nil)
	statusReq.Header.Set("Authorization", "Bearer control-token")
	status := httptest.NewRecorder()
	server.Router().ServeHTTP(status, statusReq)
	if status.Code != http.StatusOK {
		t.Fatalf("exact receipt status = %d %s", status.Code, status.Body.String())
	}
	if got := send(body, receipt); got.Code != http.StatusConflict {
		t.Fatalf("replayed protected create = %d %s", got.Code, got.Body.String())
	}
	if len(mgr.ListInstances()) != 1 {
		t.Fatal("receipt did not create exactly one instance")
	}
}
