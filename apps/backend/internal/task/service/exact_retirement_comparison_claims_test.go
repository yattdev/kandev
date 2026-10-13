package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/task/models"
)

// Contract coverage: valid current comparison metadata and caller claims do not
// substitute for the unavailable historical preservation/Git/queue adapters.
func TestPreviewExactRetirementDoesNotPromoteMutableComparisonClaims(t *testing.T) {
	svc, _, repo := createTestService(t)
	ctx := context.Background()
	seedComparisonServiceWorkspace(t, repo, "contributor/widget", "head-42")
	old, err := svc.CreateTask(ctx, &CreateTaskRequest{
		WorkspaceID: "ws-comparison", WorkflowID: "wf-comparison", WorkflowStepID: "step-comparison", Title: "Original",
		Repositories: []TaskRepositoryInput{{RepositoryID: "repo-comparison", BaseBranch: "main", CheckoutBranch: "feature/cursor-cost"}},
	})
	require.NoError(t, err)
	replacement, err := svc.CreateTask(ctx, &CreateTaskRequest{
		WorkspaceID: "ws-comparison", WorkflowID: "wf-comparison", WorkflowStepID: "step-comparison", Title: "Replacement",
	})
	require.NoError(t, err)
	target, err := comparisonCandidate("contributor/widget", "head-42", "upstream/widget", "base-99").Build()
	require.NoError(t, err)
	contribution := models.RemoteContribution{
		Version: models.RemoteContributionVersion, Provider: target.Provider, Kind: target.Kind, Number: target.Number,
		CanonicalURL: "https://github.com/upstream/widget/pull/1154", State: models.RemoteContributionStateOpen,
		BaseBranch: target.TargetBranch, HeadBranch: target.HeadBranch, HeadSHA: strings.Repeat("a", 40),
		SourceRepository: target.HeadRepository, CollaborationAllowed: true,
	}
	require.NoError(t, models.ValidatePRBaseContributionIdentity(&models.PRBase{Target: target, OID: strings.Repeat("b", 40)}, &contribution))
	rows, err := repo.ListTaskRepositories(ctx, old.Task.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	if rows[0].Metadata == nil {
		rows[0].Metadata = make(map[string]interface{})
	}
	require.NoError(t, models.PutComparisonTarget(rows[0].Metadata, &target))
	require.NoError(t, models.PutRemoteContribution(rows[0].Metadata, &contribution))
	// These are untrusted fixture claims, not a production evidence schema.
	rows[0].Metadata["claimed_historical_evidence"] = map[string]interface{}{
		"complete": true, "authenticated": true, "accepted_tree_equal": true,
		"task_id": old.Task.ID, "head_oid": contribution.HeadSHA, "tree_oid": strings.Repeat("c", 40),
		"index_preserved": true, "queues_preserved": true,
	}
	require.NoError(t, repo.UpdateTaskRepository(ctx, rows[0]))
	before, err := repo.ListTaskRepositories(ctx, old.Task.ID)
	require.NoError(t, err)
	oldTask, err := svc.GetTask(ctx, old.Task.ID)
	require.NoError(t, err)
	replacementTask, err := svc.GetTask(ctx, replacement.Task.ID)
	require.NoError(t, err)
	preview, err := svc.PreviewExactRetirement(ctxSynthetic(), ExactRetirementPreviewRequest{
		OldTaskID: old.Task.ID, ReplacementTaskID: replacement.Task.ID, WorkspaceID: "ws-comparison",
		ExpectedOldGeneration: exactRetirementGeneration(oldTask), ExpectedReplacementGeneration: exactRetirementGeneration(replacementTask),
	})
	require.NoError(t, err)
	require.False(t, preview.Eligible)
	for _, receipt := range preview.Receipts {
		if receipt.Predicate == ExactRetirementIdentityPredicate {
			require.Equal(t, ExactRetirementReceiptPass, receipt.Status)
			continue
		}
		require.Equal(t, ExactRetirementReceiptUnknown, receipt.Status)
		require.Equal(t, "INVENTORY_UNAVAILABLE", receipt.ReasonCode)
	}
	after, err := repo.ListTaskRepositories(ctx, old.Task.ID)
	require.NoError(t, err)
	require.Equal(t, before, after, "preview must not backfill or rewrite comparison claims")
}
