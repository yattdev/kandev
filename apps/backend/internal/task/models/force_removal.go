package models

import "time"

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
