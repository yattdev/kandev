package sqlite

import (
	"context"
	"testing"

	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/task/repository/queuepurge"
)

func TestUnknownQueuePurgeOnlySettlesItsPreparedOperation(t *testing.T) {
	ctx := context.Background()
	r := &Repository{}
	var aborted, purged int
	r.SetTaskQueuePurgeAborter(func(context.Context, string) { aborted++ })
	r.SetTaskQueuePurger(func(context.Context, string) { purged++ })
	retain := func(operationID string) {
		tx := &sqlx.Tx{}
		r.recordTaskQueuePurgeTx(queuepurge.WithTaskQueuePurgeOperations(ctx,
			map[string]string{"task": operationID}), tx, "task")
		r.finishTaskQueuePurgeTx(ctx, tx, false, true)
	}
	retain("archive:A")
	r.ResolveTaskQueuePurgeForOperation(ctx, "task", "archive:A", false)
	retain("archive:B")
	r.ResolveTaskQueuePurgeForOperation(ctx, "task", "archive:A", false) // stale periodic reload
	if aborted != 1 || purged != 0 {
		t.Fatalf("stale A touched B: aborted=%d purged=%d", aborted, purged)
	}
	if r.queuePurgeUnknown[(taskQueuePurgeIdentity{taskID: "task", operationID: "archive:B"}).key()] != 1 {
		t.Fatal("B's unknown purge was consumed")
	}
	r.ResolveTaskQueuePurgeForOperation(ctx, "task", "archive:B", true)
	r.ResolveTaskQueuePurgeForOperation(ctx, "task", "archive:B", true) // replay
	if aborted != 1 || purged != 1 || len(r.queuePurgeUnknown) != 0 {
		t.Fatalf("B settlement not one-shot: aborted=%d purged=%d unknown=%v", aborted, purged, r.queuePurgeUnknown)
	}
	// In the reverse winner order, settling B cannot consume A, either.
	retain("archive:C")
	retain("archive:D")
	r.ResolveTaskQueuePurgeForOperation(ctx, "task", "archive:D", true)
	if r.queuePurgeUnknown[(taskQueuePurgeIdentity{taskID: "task", operationID: "archive:C"}).key()] != 1 {
		t.Fatal("settling later D consumed earlier C")
	}
	r.ResolveTaskQueuePurgeForOperation(ctx, "task", "archive:C", false)
	if aborted != 2 || purged != 2 || len(r.queuePurgeUnknown) != 0 {
		t.Fatalf("reverse order settlement: aborted=%d purged=%d unknown=%v", aborted, purged, r.queuePurgeUnknown)
	}
}
