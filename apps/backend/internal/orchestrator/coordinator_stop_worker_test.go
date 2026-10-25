package orchestrator

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCoordinatorStopWorkerSerializesAndJoinsShutdown(t *testing.T) {
	ticks := make(chan time.Time, 4)
	worker := &coordinatorStopWorker{ticks: ticks}
	entered, release := make(chan struct{}, 4), make(chan struct{})
	var calls atomic.Int32
	cycle := func(ctx context.Context) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
	}
	worker.start(t.Context(), cycle)
	worker.start(t.Context(), cycle)
	ticks <- time.Now()
	<-entered
	ticks <- time.Now()
	require.EqualValues(t, 1, calls.Load(), "a blocked cycle prevents overlapping work")
	worker.stop()
	require.EqualValues(t, 1, calls.Load())
	select {
	case <-worker.done:
	default:
		t.Fatal("Stop must join the worker")
	}
	ticks <- time.Now()
	require.EqualValues(t, 1, calls.Load(), "shutdown leaves no worker to consume ticks")
	worker.stop()
}

func TestCoordinatorStopWorkerJoinsParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	ticks := make(chan time.Time, 1)
	worker := &coordinatorStopWorker{ticks: ticks}
	entered := make(chan struct{})
	worker.start(ctx, func(ctx context.Context) { close(entered); <-ctx.Done() })
	ticks <- time.Now()
	<-entered
	cancel()
	worker.stop()
	select {
	case <-worker.done:
	default:
		t.Fatal("parent cancellation must terminate the cycle")
	}
}
