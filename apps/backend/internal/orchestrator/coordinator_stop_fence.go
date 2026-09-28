package orchestrator

import (
	"context"
	"fmt"

	"github.com/kandev/kandev/internal/agent/runtime"
	"github.com/kandev/kandev/internal/task/models"
	taskrepo "github.com/kandev/kandev/internal/task/repository"
)

type CoordinatorStopFenceRuntime interface {
	CloseExecutionAdmission(context.Context, string, uint64) (*runtime.ExecutionFenceReceipt, error)
}

// consumeCoordinatorStopFence persists a bounded exact fence result. It never
// promotes stopped because terminal executor proof belongs to a later gate.
func (s *Service) ConsumeCoordinatorStopFence(ctx context.Context, op *models.CoordinatorStopOperation) (*models.CoordinatorStopOperation, error) {
	if op == nil {
		return nil, fmt.Errorf("coordinator stop operation is nil")
	}
	repo, ok := s.repo.(taskrepo.CoordinatorStopOperationRepository)
	if !ok {
		return nil, fmt.Errorf("coordinator stop repository is unavailable")
	}
	runtime, ok := s.agentManager.(CoordinatorStopFenceRuntime)
	if !ok {
		updated, _, err := repo.MarkCoordinatorStopOperationIncomplete(ctx, op.ID, op.ExecutionID, op.AgentctlGeneration, "agentctl_unreachable")
		return updated, err
	}
	receipt, err := runtime.CloseExecutionAdmission(ctx, op.ExecutionID, op.AgentctlGeneration)
	if err != nil {
		updated, _, markErr := repo.MarkCoordinatorStopOperationIncomplete(ctx, op.ID, op.ExecutionID, op.AgentctlGeneration, "agentctl_unreachable")
		if markErr != nil {
			return nil, markErr
		}
		return updated, nil
	}
	return repo.ConsumeCoordinatorStopFenceReceipt(ctx, op.ID, models.CoordinatorStopFenceReceipt{ExecutionID: receipt.ExecutionID, AgentctlGeneration: receipt.AgentctlGeneration, AdmissionClosedAt: receipt.AdmissionClosedAt, ManagedProcessesDrained: receipt.ManagedProcessesDrained})
}
