package models

// ExactRetirementPredicate is a closed inventory category. New evidence must
// be added explicitly so an unavailable owner cannot appear safe by omission.
// It is shared by guarded paired retirement and safe force-removal quarantine.
type ExactRetirementPredicate string

const (
	ExactRetirementIdentityPredicate     ExactRetirementPredicate = "identity"
	ExactRetirementQueuePredicate        ExactRetirementPredicate = "session_queue"
	ExactRetirementMovePredicate         ExactRetirementPredicate = "move_dispatch"
	ExactRetirementRelationshipPredicate ExactRetirementPredicate = "relationships"
	ExactRetirementPRPredicate           ExactRetirementPredicate = "pr_watch"
	ExactRetirementPreservationPredicate ExactRetirementPredicate = "preservation"
	ExactRetirementGitPredicate          ExactRetirementPredicate = "git"
	ExactRetirementEnvironmentPredicate  ExactRetirementPredicate = "environment_runtime"
	ExactRetirementConsumerPredicate     ExactRetirementPredicate = "lease_consumer"
	ExactRetirementOwnershipPredicate    ExactRetirementPredicate = "replacement_ownership"
)

type ExactRetirementReceiptStatus string

const (
	ExactRetirementReceiptPass    ExactRetirementReceiptStatus = "PASS"
	ExactRetirementReceiptBlocked ExactRetirementReceiptStatus = "BLOCKED"
	ExactRetirementReceiptUnknown ExactRetirementReceiptStatus = "UNKNOWN"
)

// ExactRetirementPredicateReceipt contains only fixed-category evidence. It
// must not carry queue bodies, source bytes, credentials, or provider tokens.
type ExactRetirementPredicateReceipt struct {
	Predicate          ExactRetirementPredicate     `json:"predicate"`
	Status             ExactRetirementReceiptStatus `json:"status"`
	ReasonCode         string                       `json:"reason_code"`
	ResourceID         string                       `json:"resource_id"`
	ObservedGeneration string                       `json:"observed_generation"`
	EvidenceDigest     string                       `json:"evidence_digest"`
}
