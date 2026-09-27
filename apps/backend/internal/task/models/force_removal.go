package models

import (
	"errors"
	"time"
)

// ErrForceRemovalTaskHeld rejects a writer after an exact-task removal claim.
// It lives in models so task-owned repositories can share the sentinel without
// introducing an import cycle through the task SQLite repository.
var ErrForceRemovalTaskHeld = errors.New("force removal task is held")

// ForceRemovalClaim is the durable, exact-task admission fence. It is private
// repository state until force-removal service authorization is implemented.
type ForceRemovalClaim struct {
	TaskID              string
	WorkspaceID         string
	TaskGeneration      time.Time
	AdmissionGeneration string
	OperationID         string
	RequestDigest       string
	PreviewDigest       string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}
