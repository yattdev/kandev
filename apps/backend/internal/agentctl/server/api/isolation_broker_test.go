package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kandev/kandev/internal/agentctl/server/config"
	"github.com/kandev/kandev/internal/common/logger"
)

func TestProtectedCreateRequiresFreshBrokerReceipt(t *testing.T) {
	server := NewControlServer(&config.Config{AuthToken: "current-control-token"}, nil, logger.Default())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/instances", strings.NewReader(`{"id":"instance","workspace_path":"/tmp/workspace","workspace_isolation":{}}`))
	req.Header.Set("Authorization", "Bearer current-control-token")
	resp := httptest.NewRecorder()
	server.Router().ServeHTTP(resp, req)
	if resp.Code != http.StatusConflict || !strings.Contains(resp.Body.String(), "broker") {
		t.Fatalf("protected create without broker = %d %s, want actionable fail-closed response", resp.Code, resp.Body.String())
	}
}
