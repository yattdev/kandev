package taskpreview

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestOwnershipLossBeforeDialSendsNoTrialRequest(t *testing.T) {
	f := newFixture(t)
	f.upstream.Close()
	var requests, inspections atomic.Int64
	f.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(f.upstream.Close)
	u, _ := url.Parse(f.upstream.URL)
	f.grant.HostPort, _ = strconv.Atoi(u.Port())
	f.save(t)
	original := f.control.Inspector
	f.control.Inspector = inspectFunc(func(ctx context.Context, g Grant) error {
		inspections.Add(1)
		err := original.Validate(ctx, g)
		// The first answer was valid when read, but ownership changes before
		// the gateway receives that answer and establishes the target socket.
		if err == nil {
			f.container.Store(false)
		}
		return err
	})
	w := f.request(http.MethodGet, "/", f.session())
	if requests.Load() != 0 || inspections.Load() < 2 || w.Code != http.StatusBadGateway {
		t.Fatalf("ownership changed before dial: requests=%d inspections=%d status=%d", requests.Load(), inspections.Load(), w.Code)
	}
}

func TestSameContainerRestartDoesNotRetainGrant(t *testing.T) {
	f := newFixture(t)
	started := time.Unix(f.clock.Load()-60, 0).UTC().Format(time.RFC3339Nano)
	record := map[string]any{"id": f.grant.ContainerID, "image": f.grant.ImageID,
		"running": true, "started_at": started,
		"labels": map[string]string{"com.docker.compose.project": f.grant.Project, "com.docker.compose.service": f.grant.Service},
		"ports":  map[string]any{"59430/tcp": []map[string]string{{"HostIp": "127.0.0.1", "HostPort": strconv.Itoa(f.grant.HostPort)}}}}
	raw, _ := json.Marshal(record)
	if ValidateInspection(raw, f.grant) != nil {
		t.Fatal("original container boot denied")
	}
	record["started_at"] = time.Unix(f.clock.Load(), 0).UTC().Format(time.RFC3339Nano)
	raw, _ = json.Marshal(record)
	if ValidateInspection(raw, f.grant) == nil {
		t.Fatal("a restarted container retained the original grant")
	}
}

func TestContainerBootTimestampRequiredForGrant(t *testing.T) {
	f := newFixture(t)
	for _, value := range []string{"", "invalid", time.Unix(f.grant.IssuedAt+60, 0).UTC().Format(time.RFC3339Nano)} {
		grant := f.grant
		grant.ContainerStartedAt = value
		if grant.Validate() == nil {
			t.Fatal("missing, invalid or future container boot was accepted")
		}
	}
}
