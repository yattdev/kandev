package dependencywrite

import (
	"context"
	"errors"

	"github.com/jmoiron/sqlx"
)

var ErrCycle = errors.New("dependency cycle")

// DependencyMutationRequest carries server-attested identities for one edge
// operation. Endpoint IDs are caller input and must be authorized inside the
// writer's transaction before they can enter a success audit row.
type DependencyMutationRequest struct {
	Operation         string
	WorkspaceID       string
	DependentTaskID   string
	PredecessorTaskID string
	CallerTaskID      string
	CallerSessionID   string
	CallerExecutionID string
	CallerUserID      string
	OriginClass       string
	CorrelationID     string
}

type DependencyMutationResult struct {
	Changed             bool
	DependentRevision   int64
	PredecessorRevision int64
}

// DependencyMutationAuthorizer runs after the writer takes its workspace
// barrier and before graph reads or writes. It must reject stale callers and
// out-of-scope endpoints using the same transaction.
type DependencyMutationAuthorizer func(context.Context, *sqlx.Tx) error
