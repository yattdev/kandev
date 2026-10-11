package github

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
)

func restoreTaskPRTx(ctx context.Context, tx *sqlx.Tx, taskID, repositoryID string, status *PRStatus) (*TaskPR, error) {
	pr := status.PR
	var outgoing TaskPR
	err := tx.GetContext(ctx, &outgoing,
		tx.Rebind(`SELECT `+taskPRColumns+` FROM github_task_prs
			 WHERE task_id = ? AND repository_id = ? AND pr_number = ?`),
		taskID, repositoryID, pr.Number)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err == nil {
		if err := hydrateTaskPRWorkflowAttention(&outgoing); err != nil {
			return nil, err
		}
	}

	isDraft, changedFiles, mergedByLogin, closedByLogin, autoMergeObservedAt :=
		resolveTaskPROutcomeFields(&outgoing, status)
	queue := resolveTaskPRMergeQueueState(&outgoing, status)
	headSHA := outgoing.HeadSHA
	if pr.HeadSHA != "" {
		headSHA = pr.HeadSHA
	}
	workflowAttention := resolveTaskPRWorkflowAttention(&outgoing, status, headSHA)

	if _, err := tx.ExecContext(ctx, tx.Rebind(
		`UPDATE github_task_prs SET owner = ?, repo = ?, pr_url = ?, pr_title = ?,
			head_branch = ?, base_branch = ?, head_sha = ?, author_login = ?, state = ?, mergeable_state = ?, has_merge_conflicts = ?,
			merge_queue_state = ?, merge_queue_position = ?, merge_queue_entry_id = ?, merge_queue_entry_head_sha = ?, merge_queue_estimated_time_to_merge_seconds = ?,
			merge_queue_last_removal_id = ?, merge_queue_last_removed_at = ?, merge_queue_last_removal_reason = ?, merge_queue_last_removal_before_sha = ?,
			additions = ?, deletions = ?, merged_at = ?, closed_at = ?, detached_at = NULL, updated_at = ?,
			is_draft = ?, changed_files = ?, merged_by_login = ?, closed_by_login = ?,
			auto_merge_observed_at = COALESCE(auto_merge_observed_at, ?), workflow_attention = ?
		 WHERE task_id = ? AND repository_id = ? AND pr_number = ?`),
		pr.RepoOwner, pr.RepoName, pr.HTMLURL, pr.Title, pr.HeadBranch, pr.BaseBranch, headSHA, pr.AuthorLogin,
		pr.State, effectivePRMergeableState(pr), observedTaskPRMergeConflict(outgoing.HasMergeConflicts, pr, pr.MergeableState), queue.state, queue.position, queue.entryID, queue.entryHeadSHA, queue.estimate,
		queue.lastRemovalID, queue.lastRemovedAt, queue.lastRemovalReason, queue.lastRemovalBeforeSHA,
		pr.Additions, pr.Deletions, pr.MergedAt, pr.ClosedAt, time.Now().UTC(),
		isDraft, changedFiles, mergedByLogin, closedByLogin, autoMergeObservedAt,
		marshalWorkflowAttention(workflowAttention),
		taskID, repositoryID, pr.Number); err != nil {
		return nil, err
	}
	var row TaskPR
	if err := tx.GetContext(ctx, &row, tx.Rebind(`SELECT `+taskPRColumns+` FROM github_task_prs WHERE task_id = ? AND repository_id = ? AND pr_number = ?`), taskID, repositoryID, pr.Number); err != nil {
		return nil, err
	}
	return &row, hydrateTaskPRWorkflowAttention(&row)
}
