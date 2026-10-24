package queuepurge

import "context"

type queuePurgeOperationsKey struct{}

// WithTaskQueuePurgeOperations binds a mutation's queue purge to the prepared
// cleanup operations that admitted it. The copy keeps caller mutations from
// changing the identity while a repository transaction is in flight.
func WithTaskQueuePurgeOperations(ctx context.Context, operations map[string]string) context.Context {
	bound := make(map[string]string, len(operations))
	for taskID, operationID := range operations {
		if taskID != "" && operationID != "" {
			bound[taskID] = operationID
		}
	}
	return context.WithValue(ctx, queuePurgeOperationsKey{}, bound)
}

// TaskQueuePurgeOperation returns the exact prepared operation for a task.
func TaskQueuePurgeOperation(ctx context.Context, taskID string) string {
	operations, _ := ctx.Value(queuePurgeOperationsKey{}).(map[string]string)
	return operations[taskID]
}
