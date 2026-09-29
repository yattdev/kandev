package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/agentctl/server/config"
	"github.com/kandev/kandev/internal/agentctl/server/process"
	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/pkg/agent"
)

func TestGracefulStopFencesDelayedManagedCommandAndDrainsAgent(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	backendDir := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "../../../../"))
	mockAgent := filepath.Join(t.TempDir(), "mock-agent")
	build := exec.Command("go", "build", "-o", mockAgent, "./cmd/mock-agent")
	build.Dir = backendDir
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build mock ACP agent: %v\n%s", err, output)
	}

	workDir := t.TempDir()
	cfg := &config.InstanceConfig{Port: 45329, WorkDir: workDir, Protocol: agent.ProtocolACP}
	processManager := process.NewManager(cfg, logger.Default())
	t.Cleanup(func() { _ = processManager.StopForTeardown(context.Background()) })
	if err := processManager.Configure(mockAgent, nil, false, nil, "default", "", nil, false); err != nil {
		t.Fatalf("configure mock agent: %v", err)
	}
	if err := processManager.Start(t.Context()); err != nil {
		t.Fatalf("start mock agent: %v", err)
	}
	if processManager.Status() != process.StatusRunning {
		t.Fatalf("agent status = %q, want running", processManager.Status())
	}

	server := NewServer(cfg, processManager, nil, nil, logger.Default())
	marker := filepath.Join(workDir, "managed-command-ran")
	command := process.StartProcessRequest{SessionID: "session-fence", Command: "touch " + marker}
	startManagedCommand := func() *httptest.ResponseRecorder {
		body, err := json.Marshal(command)
		if err != nil {
			t.Fatalf("marshal managed command: %v", err)
		}
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/processes/start", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		server.Router().ServeHTTP(recorder, request)
		return recorder
	}
	if response := startManagedCommand(); response.Code != http.StatusOK {
		t.Fatalf("positive-control managed command status = %d (body %s)", response.Code, response.Body.String())
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("positive-control managed command did not create its marker")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatalf("remove positive-control marker: %v", err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for len(processManager.ListProcesses("")) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("positive-control managed command did not reach its terminal boundary")
		}
		time.Sleep(10 * time.Millisecond)
	}

	rec := httptest.NewRecorder()
	server.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/stop/graceful", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("graceful stop status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if processManager.Status() != process.StatusStopped || len(processManager.ListProcesses("")) != 0 {
		t.Fatalf("agent status = %q, managed process count = %d; want stopped and empty", processManager.Status(), len(processManager.ListProcesses("")))
	}

	start := make(chan struct{})
	result := make(chan int, 1)
	go func() {
		<-start
		result <- startManagedCommand().Code
	}()
	close(start)
	if status := <-result; status == http.StatusOK {
		t.Fatal("managed command admission succeeded after the stop cutoff")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("post-cutoff command marker stat error = %v, want no command side effect", err)
	}
}
