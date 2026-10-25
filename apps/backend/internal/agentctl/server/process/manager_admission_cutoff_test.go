package process

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/agentctl/server/config"
	"github.com/kandev/kandev/internal/common/logger"
)

func TestCloseAdmissionGracefullyCapturesCutoffAtGateClosure(t *testing.T) {
	mgr := NewManager(&config.InstanceConfig{}, logger.Default())
	entered := make(chan struct{})
	releaseClose := make(chan struct{})
	mgr.beforeAdmissionClose = func() {
		close(entered)
		<-releaseClose
	}
	cutoffResult := make(chan time.Time, 1)
	go func() { cutoffResult <- mgr.CloseAdmissionGracefully() }()
	<-entered
	releaseAdmission, err := mgr.admitStart()
	if err != nil {
		t.Fatalf("admission while closure is paused: %v", err)
	}
	admittedAt := time.Now().UTC()
	releaseAdmission()
	close(releaseClose)
	cutoff := <-cutoffResult
	if cutoff.Before(admittedAt) {
		t.Fatalf("cutoff %s precedes admitted operation %s", cutoff, admittedAt)
	}
	mgr.beforeAdmissionClose = nil
	if repeated := mgr.CloseAdmissionGracefully(); !repeated.Equal(cutoff) {
		t.Fatalf("repeated cutoff %s, want original %s", repeated, cutoff)
	}
	if _, err := mgr.admitStart(); !errors.Is(err, ErrManagerStopping) {
		t.Fatalf("admission after closure = %v, want ErrManagerStopping", err)
	}
	if err := mgr.WaitForAdmission(context.Background()); err != nil {
		t.Fatalf("wait for admissions: %v", err)
	}
}

func TestStreamSubscriptionDoesNotBlockGracefulAdmissionDrain(t *testing.T) {
	mgr := NewManager(&config.InstanceConfig{}, logger.Default())
	streamCtx, release, err := mgr.BeginStreamSubscription(context.Background())
	if err != nil {
		t.Fatalf("begin stream: %v", err)
	}
	defer release()
	mgr.CloseAdmissionGracefully()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := mgr.WaitForAdmission(ctx); err != nil {
		t.Fatalf("stream subscription blocked command drain: %v", err)
	}
	if streamCtx.Err() != nil {
		t.Fatalf("graceful fence canceled active stream: %v", streamCtx.Err())
	}
	if _, _, err := mgr.BeginStreamSubscription(context.Background()); !errors.Is(err, ErrManagerStopping) {
		t.Fatalf("stream admission after closure = %v, want ErrManagerStopping", err)
	}
	mgr.CloseAdmission()
	select {
	case <-streamCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("teardown did not cancel active stream")
	}
}
