package handlers

import (
	"context"
	"sync"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository"
)

// handlerCleanupAdmission gives lifecycle handler fixtures the durable job
// readback that production wires through its repository.
type handlerCleanupAdmission struct {
	repository.TaskResourceCleanupRepository
	mu   sync.Mutex
	jobs map[string]*models.TaskResourceCleanupJob
}

func (r *handlerCleanupAdmission) CreateTaskResourceCleanupJob(_ context.Context, job *models.TaskResourceCleanupJob) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.jobs == nil {
		r.jobs = make(map[string]*models.TaskResourceCleanupJob)
	}
	copy := *job
	r.jobs[job.OperationID] = &copy
	return nil
}

func (r *handlerCleanupAdmission) GetTaskResourceCleanupJobByOperationID(_ context.Context, operationID string) (*models.TaskResourceCleanupJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if job := r.jobs[operationID]; job != nil {
		copy := *job
		return &copy, nil
	}
	return nil, nil
}

func (r *handlerCleanupAdmission) StartPreparedTaskResourceCleanupJob(_ context.Context, id string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, job := range r.jobs {
		if job.ID == id && job.State == models.TaskResourceCleanupStatePrepared {
			job.State = models.TaskResourceCleanupStatePending
			return true, nil
		}
	}
	return false, nil
}
