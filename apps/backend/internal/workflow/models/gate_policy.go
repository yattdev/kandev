package models

import "strings"

// NormalizeCompletionGatePolicy assigns legacy names only when creating or
// importing a step without a policy. Stored policies survive later renames.
func NormalizeCompletionGatePolicy(policy CompletionGatePolicy, name string) CompletionGatePolicy {
	if policy != "" {
		return policy
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case string(CompletionGatePolicyReview):
		return CompletionGatePolicyReview
	case string(CompletionGatePolicyQA):
		return CompletionGatePolicyQA
	default:
		return CompletionGatePolicyNone
	}
}

func (p CompletionGatePolicy) RequiresVerdict() bool {
	return p != "" && p != CompletionGatePolicyNone
}
