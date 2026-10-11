package taskinstructions

import (
	"errors"
	"time"
)

var (
	ErrBindingNotFound    = errors.New("task instruction binding not found")
	ErrVersionConflict    = errors.New("task instruction version changed")
	ErrEnvironmentChanged = errors.New("task instruction environment changed")
)

// DurableBinding is the database-authoritative binding. The filesystem
// projection and provider context are derived only after this record passes
// its environment-generation fence.
type DurableBinding struct {
	TaskID              string
	WorkspaceID         string
	EnvironmentID       string
	OwnershipGeneration int64
	Version             int64
	Content             string
	Digest              string
	ControlPath         string
	GuardKind           GuardKind
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// WriteRequest carries the caller's full compare-and-swap fence. Version zero
// means the binding must be absent.
type WriteRequest struct {
	TaskID                      string
	WorkspaceID                 string
	EnvironmentID               string
	ExpectedOwnershipGeneration int64
	ExpectedVersion             int64
	Content                     string
	Digest                      string
	ControlPath                 string
	GuardKind                   GuardKind
}

func (r WriteRequest) Validate() error {
	if r.TaskID == "" || r.WorkspaceID == "" || r.EnvironmentID == "" ||
		r.ExpectedOwnershipGeneration < 1 || r.ExpectedVersion < 0 {
		return errors.New("task instruction write fence is incomplete")
	}
	if len(r.Content) > maxContentBytes || !utf8Content(r.Content) || r.Digest != Digest(r.Content) {
		return errors.New("task instruction content or digest is invalid")
	}
	return nil
}
