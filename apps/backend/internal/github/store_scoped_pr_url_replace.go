package github

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

func replaceTaskPRTx(ctx context.Context, tx *sqlx.Tx, tp *TaskPR, status *PRStatus) (*TaskPR, error) {
	if tp.ID == "" {
		tp.ID = uuid.New().String()
	}
	tp.UpdatedAt = time.Now().UTC()
	outgoing, err := replaceTaskPROutgoingRow(ctx, tx, tp)
	if err != nil {
		return nil, err
	}
	tp.IsDraft, tp.ChangedFiles, tp.MergedByLogin, tp.ClosedByLogin, tp.AutoMergeObservedAt =
		resolveTaskPROutcomeFields(outgoing, status)
	if status != nil && status.PR != nil {
		tp.MergeableState = effectivePRMergeableState(status.PR)
		tp.HasMergeConflicts = observedTaskPRMergeConflict(outgoing.HasMergeConflicts, status.PR, status.PR.MergeableState)
	}
	queueSource := tp
	if outgoing.ID != "" {
		queueSource = outgoing
	}
	queue := resolveTaskPRMergeQueueState(queueSource, status)
	tp.MergeQueueState, tp.MergeQueuePosition, tp.MergeQueueEstimatedTimeToMergeSeconds = queue.state, queue.position, queue.estimate
	tp.MergeQueueEntryID, tp.MergeQueueEntryHeadSHA = queue.entryID, queue.entryHeadSHA
	tp.MergeQueueLastRemovalID, tp.MergeQueueLastRemovedAt = queue.lastRemovalID, queue.lastRemovedAt
	tp.MergeQueueLastRemovalReason, tp.MergeQueueLastRemovalBeforeSHA = queue.lastRemovalReason, queue.lastRemovalBeforeSHA
	if status != nil && status.PR != nil && status.PR.HeadSHA != "" {
		tp.HeadSHA = status.PR.HeadSHA
	} else if tp.HeadSHA == "" && outgoing.ID != "" {
		tp.HeadSHA = outgoing.HeadSHA
	}
	tp.WorkflowAttention = resolveTaskPRWorkflowAttention(outgoing, status, tp.HeadSHA)
	tp.WorkflowAttentionJSON = marshalWorkflowAttention(tp.WorkflowAttention)

	if tp.RepositoryID != "" {
		if _, err := tx.ExecContext(ctx, tx.Rebind(
			`DELETE FROM github_task_prs
			 WHERE task_id = ? AND repository_id = ? AND pr_number = ?`),
			tp.TaskID, tp.RepositoryID, tp.PRNumber); err != nil {
			return nil, err
		}
	} else {
		if _, err := tx.ExecContext(ctx, tx.Rebind(
			`DELETE FROM github_task_prs
			 WHERE task_id = ? AND repository_id = '' AND pr_number = ?`),
			tp.TaskID, tp.PRNumber); err != nil {
			return nil, err
		}
	}
	if err := insertTaskPR(ctx, tx, tp); err != nil {
		return nil, err
	}
	return tp, nil
}
