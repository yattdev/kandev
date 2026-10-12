package orchestrator

import (
	"context"
	"fmt"

	taskrepo "github.com/kandev/kandev/internal/task/repository"
)

func (s *Service) reconcilePendingCoordinatorStopRequestsOnStartup(ctx context.Context) error {
	repo, ok := s.repo.(taskrepo.CoordinatorStopRequestRepository)
	if !ok {
		return nil
	}
	requests, err := repo.ListPendingCoordinatorStopRequests(ctx)
	if err != nil {
		return fmt.Errorf("list pending coordinator stop requests: %w", err)
	}
	for _, request := range requests {
		if _, err := s.StopTaskForCoordinatorOperation(ctx, request.TaskID, request.ParentTaskID, request.OperationID); err != nil {
			return fmt.Errorf("resume coordinator stop request %s: %w", request.OperationID, err)
		}
	}
	return nil
}
