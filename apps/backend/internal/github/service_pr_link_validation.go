package github

import (
	"errors"
	"fmt"
	"strings"
)

// Linking records a deliverable; it does not grant permission to edit its branch.
// Keep contribution creation's open-only policy separate from link eligibility.
func validateGitHubPRLink(owner, repo string, number int, pr *PR) error {
	if err := validateGitHubPRIdentityAndRefs(owner, repo, number, pr); err != nil {
		return err
	}
	switch pr.State {
	case defaultPRState, prStateMerged:
		return nil
	default:
		return fmt.Errorf("GitHub pull request #%d is neither open nor merged", number)
	}
}

func validateExistingGitHubPRLink(existing *TaskPR, pr *PR, prURL string) error {
	if existing != nil && !strings.EqualFold(strings.TrimSuffix(existing.PRURL, "/"), strings.TrimSuffix(prURL, "/")) {
		return errors.New("a different canonical PR already uses this task repository and number")
	}
	if existing != nil && existing.DetachedAt == nil && existing.HeadSHA != pr.HeadSHA {
		return errors.New("existing PR link has a stale head SHA")
	}
	return nil
}
