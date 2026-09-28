package orchestrator

import (
	"context"
	"fmt"

	"github.com/kandev/kandev/internal/agent/runtime"
	"github.com/kandev/kandev/internal/task/models"
	taskrepo "github.com/kandev/kandev/internal/task/repository"
)

type CoordinatorStopGracefulFenceRuntime interface {
	StopExecutionWithFence(context.Context, string, uint64, string, func(*runtime.ExecutionFenceReceipt) error, func() error) error
}

// stopWithCoordinatorFence persists the lifecycle-owned receipt while the
// exact execution is pinned, then lets lifecycle gracefully stop that same
// execution before releasing its replacement lock.
func (s *Service) stopWithCoordinatorFence(ctx context.Context, op *models.CoordinatorStopOperation) error {
	if op == nil {
		return fmt.Errorf("coordinator stop operation is nil")
	}
	repo, ok := s.repo.(taskrepo.CoordinatorStopOperationRepository)
	if !ok {
		return fmt.Errorf("coordinator stop repository is unavailable")
	}
	lifecycleRuntime, ok := s.agentManager.(CoordinatorStopGracefulFenceRuntime)
	if !ok {
		_, _, err := repo.MarkCoordinatorStopOperationIncomplete(ctx, op.ID, op.ExecutionID, op.AgentctlGeneration, "exact_lifecycle_stop_unavailable")
		return err
	}
	err := lifecycleRuntime.StopExecutionWithFence(ctx, op.ExecutionID, op.AgentctlGeneration, coordinatorMCPStopReason, func(receipt *runtime.ExecutionFenceReceipt) error {
		if receipt == nil {
			return fmt.Errorf("lifecycle returned an empty execution fence receipt")
		}
		_, err := repo.ConsumeCoordinatorStopFenceReceipt(ctx, op.ID, models.CoordinatorStopFenceReceipt{
			ExecutionID: receipt.ExecutionID, AgentctlGeneration: receipt.AgentctlGeneration,
			AdmissionClosedAt: receipt.AdmissionClosedAt, ManagedProcessesDrained: receipt.ManagedProcessesDrained,
		})
		return err
	}, func() error {
		return repo.RecordCoordinatorStopLifecycleProof(ctx, op.ID, op.ExecutionID, op.AgentctlGeneration)
	})
	if err != nil {
		if _, _, markErr := repo.MarkCoordinatorStopOperationIncomplete(ctx, op.ID, op.ExecutionID, op.AgentctlGeneration, "exact_fence_or_graceful_stop_failed"); markErr != nil {
			return fmt.Errorf("record exact lifecycle stop failure: %w", markErr)
		}
	}
	return nil
}
