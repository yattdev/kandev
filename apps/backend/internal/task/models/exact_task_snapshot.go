package models

import (
	"time"

	v1 "github.com/kandev/kandev/pkg/api/v1"
)

// ExactTaskSnapshotRequest scopes a bounded, materialized task read. It has
// no free-text or cross-workspace filters, so a token cannot be repurposed for
// a wider query.
type ExactTaskSnapshotRequest struct {
	WorkspaceID      string
	IncludeArchived  bool
	IncludeEphemeral bool
	TTL              time.Duration
}

// ExactTaskSnapshot identifies a server-materialized task projection. Token
// is random, opaque and usable only until ExpiresAt while its workspace fence
// remains unchanged.
type ExactTaskSnapshot struct {
	Token       string
	WorkspaceID string
	ExpiresAt   time.Time
}

// ExactTaskSnapshotTask is deliberately narrower than Task. Metadata,
// repository attachments and execution configuration do not belong to the
// generic exact task read boundary.
type ExactTaskSnapshotTask struct {
	ID              string
	WorkspaceID     string
	WorkflowID      string
	WorkflowStepID  string
	Title           string
	Description     string
	State           v1.TaskState
	Priority        string
	Position        int
	Archived        bool
	ResourceVersion int64
}

// ExactSessionSnapshotRequest scopes a complete, materialized session read to
// one workspace. Sessions belonging to archived tasks remain part of the
// history, so this boundary has no archive filter.
type ExactSessionSnapshotRequest struct {
	WorkspaceID string
	TTL         time.Duration
}

// ExactSessionSnapshot identifies a server-materialized session projection.
type ExactSessionSnapshot struct {
	Token       string
	WorkspaceID string
	ExpiresAt   time.Time
}

// ExactSessionSnapshotSession is the public-safe session identity and
// lifecycle projection. Credentials, workspace paths, metadata, and execution
// configuration remain outside this generic read boundary.
type ExactSessionSnapshotSession struct {
	ID                 string
	TaskID             string
	WorkspaceID        string
	QueueIncarnationID string
	State              TaskSessionState
	RouteGeneration    int64
	StartedAt          time.Time
	CompletedAt        *time.Time
	UpdatedAt          time.Time
	IsPrimary          bool
	ResourceVersion    int64
}

// ExactSessionMessageSnapshotRequest binds a sanitized, materialized message
// projection to the installation and exact session lifecycle identity that
// authorized it. It has no filters because the projection is complete.
type ExactSessionMessageSnapshotRequest struct {
	InstallationID         string
	WorkspaceID            string
	TaskID                 string
	SessionID              string
	QueueIncarnationID     string
	RouteGeneration        int64
	SessionResourceVersion int64
	TTL                    time.Duration
}

// ExactSessionMessageSnapshot identifies a server-materialized sanitized
// message projection. The token is opaque and is usable only with its exact
// bound identity until ExpiresAt.
type ExactSessionMessageSnapshot struct {
	Token                  string
	InstallationID         string
	WorkspaceID            string
	TaskID                 string
	SessionID              string
	QueueIncarnationID     string
	RouteGeneration        int64
	SessionResourceVersion int64
	ExpiresAt              time.Time
}

// ExactSessionMessageSnapshotPageRequest repeats the identity at the read
// boundary so a captured token cannot be replayed by another installation or
// against a replacement session generation.
type ExactSessionMessageSnapshotPageRequest struct {
	ExactSessionMessageSnapshotRequest
	Token  string
	Offset int
	Limit  int
}

// ExactSessionMessageSnapshotMessage is the deliberately narrow, sanitized
// message projection. It excludes author identifiers, turn identities and
// metadata, which can carry provider or credential material.
type ExactSessionMessageSnapshotMessage struct {
	ID            string
	AuthorType    MessageAuthorType
	Content       string
	Type          MessageType
	RequestsInput bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
