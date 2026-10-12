package orchestrator

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
)

const coordinatorStopReconcileInterval = 30 * time.Second

type coordinatorStopWorker struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	ticks  <-chan time.Time
}

func (w *coordinatorStopWorker) start(parent context.Context, cycle func(context.Context)) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	w.cancel, w.done = cancel, make(chan struct{})
	done := w.done
	go func() {
		defer close(done)
		ticks := w.ticks
		if ticks == nil {
			ticker := time.NewTicker(coordinatorStopReconcileInterval)
			defer ticker.Stop()
			ticks = ticker.C
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticks:
				if ctx.Err() != nil {
					return
				}
				cycleCtx, release := context.WithTimeout(ctx, coordinatorStopReconcileInterval)
				cycle(cycleCtx)
				release()
			}
		}
	}()
}

func (w *coordinatorStopWorker) stop() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cancel != nil {
		w.cancel()
		<-w.done
		w.cancel = nil
	}
}

func (s *Service) reconcileCoordinatorStopsOnce(ctx context.Context) {
	if err := s.reconcilePendingCoordinatorStopRequestsOnStartup(ctx); err != nil && ctx.Err() == nil {
		s.logger.Warn("failed to reconcile pending coordinator stops", zap.Error(err))
	}
}
