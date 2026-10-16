package executor

import (
	"context"
	"errors"

	"github.com/kandev/kandev/internal/agent/runtime/routingerr"
)

// ErrStartupSweepStopped identifies cancellation by the service that owns
// startup recovery, separately from a user/request cancellation.
var ErrStartupSweepStopped = errors.New("startup lifecycle sweep stopped")

func (e *Executor) handlePreparedLaunchFailure(ctx context.Context, req *LaunchAgentRequest,
	repositoryID, taskRepositoryID string, launchErr error) error {
	// A cancelled attachment has not started an agent or admitted a turn. Leave
	// its state unchanged; the next startup must still pass exact-route admission.
	if !req.StartAgent && req.WorkspaceReuseRequired &&
		errors.Is(context.Cause(ctx), ErrStartupSweepStopped) && errors.Is(launchErr, context.Canceled) {
		return routingerr.SanitizeError(launchErr)
	}
	return e.handleLaunchFailure(ctx, req.TaskID, req.SessionID, repositoryID, taskRepositoryID, launchErr)
}
