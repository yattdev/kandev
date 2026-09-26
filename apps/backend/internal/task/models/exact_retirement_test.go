package models

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// @covers AC-TASKS-SAFE-FORCE-REMOVAL-004.2
func TestExactRetirementPredicateReceiptUsesStableRedactedContract(t *testing.T) {
	receipt := ExactRetirementPredicateReceipt{
		Predicate:          ExactRetirementIdentityPredicate,
		Status:             ExactRetirementReceiptPass,
		ReasonCode:         "EXACT_PAIR_AUTHORIZED",
		ResourceID:         "task-id",
		ObservedGeneration: "generation",
		EvidenceDigest:     "digest",
	}

	body, err := json.Marshal(receipt)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"predicate":"identity",
		"status":"PASS",
		"reason_code":"EXACT_PAIR_AUTHORIZED",
		"resource_id":"task-id",
		"observed_generation":"generation",
		"evidence_digest":"digest"
	}`, string(body))
}
