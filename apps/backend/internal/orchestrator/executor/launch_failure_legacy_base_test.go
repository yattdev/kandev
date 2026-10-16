package executor

import (
	"errors"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestLegacyWorkspaceBaseFailureRetainsWorkspaceClassification(t *testing.T) {
	for _, scenario := range []struct {
		message string
		code    string
	}{
		{"environment preparation failed: base branch does not exist", models.LaunchErrorCategoryBaseBranchMissing},
		{"base branch does not exist", models.LaunchErrorCategoryGenericLaunchFailure},
		{"agent reported environment preparation failed: base branch does not exist", models.LaunchErrorCategoryGenericLaunchFailure},
		{"environment preparation failed: base branch does not exist (provider error)", models.LaunchErrorCategoryGenericLaunchFailure},
	} {
		t.Run(scenario.message, func(t *testing.T) {
			got := classifyLaunchFailure(errors.New(scenario.message))
			require.Equal(t, scenario.code, got.code)
		})
	}
}
