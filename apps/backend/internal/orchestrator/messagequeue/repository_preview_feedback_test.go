package messagequeue_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/task/models"
	tasksqlite "github.com/kandev/kandev/internal/task/repository/sqlite"
)

type taskFeedbackQueueWriter interface {
	InsertWithTaskFeedback(
		context.Context,
		messagequeue.QueueSessionIdentity,
		*messagequeue.QueuedMessage,
		[]models.TaskPlanCommentRef,
		[]models.TaskPreviewFeedbackRef,
		string,
		bool,
		*messagequeue.QueueAttachmentClaim,
		int,
	) (*models.TaskPlanCommentSnapshot, *models.TaskPreviewFeedbackSnapshot, bool, error)
}

func TestSQLiteRepositoryInsertWithTaskFeedbackQueuesAndConsumesMixedContext(t *testing.T) {
	taskRepo, queueRepo := newPlanCommentQueueRepos(t)
	ctx := context.Background()
	seedQueuePlanComment(t, ctx, taskRepo, "preview")
	preview := seedQueuePreviewFeedback(t, ctx, taskRepo, "preview")
	queued := planCommentQueuedMessage(
		"preview", "queue-preview", "fingerprint-preview",
		[]models.TaskPlanCommentRef{{ID: "comment-preview", Version: 1}},
	)
	queued.Metadata["preview_feedback_refs"] = []models.TaskPreviewFeedbackRef{{ID: preview.ID, Version: 1}}
	identity := resolvePlanCommentQueueIdentity(t, ctx, queueRepo, queued)
	writer, ok := queueRepo.(taskFeedbackQueueWriter)
	if !ok {
		t.Fatal("queue repository does not implement task feedback admission")
	}

	planSnapshot, previewSnapshot, replay, err := writer.InsertWithTaskFeedback(
		ctx, identity, queued,
		[]models.TaskPlanCommentRef{{ID: "comment-preview", Version: 1}},
		[]models.TaskPreviewFeedbackRef{{ID: preview.ID, Version: 1}},
		"fingerprint-preview",
		true, nil, 10,
	)
	if err != nil || replay {
		t.Fatalf("InsertWithTaskFeedback replay=%v err=%v", replay, err)
	}
	if planSnapshot == nil || len(planSnapshot.Comments) != 0 ||
		previewSnapshot == nil || len(previewSnapshot.Items) != 0 {
		t.Fatalf("snapshots plan=%#v preview=%#v", planSnapshot, previewSnapshot)
	}
	for _, want := range []string{
		"### Plan Comments", "typed content", "### Web Preview Feedback",
		"Runtime queue copy", "node_path",
	} {
		if !strings.Contains(queued.Content, want) {
			t.Fatalf("queued content missing %q:\n%s", want, queued.Content)
		}
	}
	entries, err := queueRepo.ListBySession(ctx, queued.SessionID)
	if err != nil || len(entries) != 1 || entries[0].Content != queued.Content {
		t.Fatalf("stored queue=%#v err=%v", entries, err)
	}
}

func TestSQLiteTaskFeedbackAdmissionAdvancesCensusButExactReplayDoesNot(t *testing.T) {
	taskRepo, queueRepo := newPlanCommentQueueRepos(t)
	ctx := context.Background()
	seedQueuePlanComment(t, ctx, taskRepo, "census-feedback")
	preview := seedQueuePreviewFeedback(t, ctx, taskRepo, "census-feedback")
	refs := []models.TaskPlanCommentRef{{ID: "comment-census-feedback", Version: 1}}
	previewRefs := []models.TaskPreviewFeedbackRef{{ID: preview.ID, Version: 1}}
	message := planCommentQueuedMessage("census-feedback", "queue-census-feedback", "fingerprint-census-feedback", refs)
	message.Metadata["preview_feedback_refs"] = previewRefs
	identity := resolvePlanCommentQueueIdentity(t, ctx, queueRepo, message)
	if _, err := taskRepo.DB().ExecContext(ctx, `CREATE TABLE IF NOT EXISTS executors_running (session_id TEXT PRIMARY KEY, agent_execution_id TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := taskRepo.DB().ExecContext(ctx, `INSERT INTO executors_running
		(id, session_id, task_id, executor_id, agent_execution_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, "executor-census-feedback", identity.SessionID, identity.TaskID, "executor-test", "execution-1", now, now); err != nil {
		t.Fatal(err)
	}
	observer := queueRepo.(interface {
		ObservePendingCensus(context.Context, messagequeue.QueueSessionIdentity, string, int) (messagequeue.PendingCensus, error)
	})
	before, err := observer.ObservePendingCensus(ctx, identity, "execution-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	writer := queueRepo.(taskFeedbackQueueWriter)
	if _, _, replay, err := writer.InsertWithTaskFeedback(ctx, identity, message, refs, previewRefs, "fingerprint-census-feedback", true, nil, 10); err != nil || replay {
		t.Fatalf("first feedback admission replay=%v err=%v", replay, err)
	}
	after, err := observer.ObservePendingCensus(ctx, identity, "execution-1", 10)
	if err != nil || len(after.Entries) != 1 || after.Revision <= before.Revision {
		t.Fatalf("feedback census after insert=%#v err=%v", after, err)
	}
	retry := planCommentQueuedMessage("census-feedback", message.ID, "fingerprint-census-feedback", refs)
	retry.Metadata["preview_feedback_refs"] = previewRefs
	if _, _, replay, err := writer.InsertWithTaskFeedback(ctx, identity, retry, refs, previewRefs, "fingerprint-census-feedback", true, nil, 10); err != nil || !replay {
		t.Fatalf("feedback replay=%v err=%v", replay, err)
	}
	unchanged, err := observer.ObservePendingCensus(ctx, identity, "execution-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Revision != after.Revision || unchanged.Entries[0].ClaimSHA256 != after.Entries[0].ClaimSHA256 {
		t.Fatalf("exact feedback replay changed census claim: before=%#v after=%#v", after, unchanged)
	}
}

func TestSQLiteRepositoryTaskFeedbackReplaySurvivesQueueDrain(t *testing.T) {
	taskRepo, queueRepo := newPlanCommentQueueRepos(t)
	ctx := context.Background()
	seedQueuePlanComment(t, ctx, taskRepo, "preview-drained")
	preview := seedQueuePreviewFeedback(t, ctx, taskRepo, "preview-drained")
	refs := []models.TaskPreviewFeedbackRef{{ID: preview.ID, Version: 1}}
	queued := planCommentQueuedMessage("preview-drained", "queue-preview-drained", "fingerprint-preview-drained", nil)
	queued.Metadata["preview_feedback_refs"] = refs
	identity := resolvePlanCommentQueueIdentity(t, ctx, queueRepo, queued)
	writer, ok := queueRepo.(taskFeedbackQueueWriter)
	if !ok {
		t.Fatal("queue repository does not implement task feedback admission")
	}

	_, _, replay, err := writer.InsertWithTaskFeedback(
		ctx, identity, queued, nil, refs, "fingerprint-preview-drained", false, nil, 10,
	)
	if err != nil || replay {
		t.Fatalf("first insert replay=%v err=%v", replay, err)
	}
	reserved, autoRun, err := queueRepo.ReserveHeadIfAutoRunForSession(ctx, identity)
	if err != nil || !autoRun || reserved == nil {
		t.Fatalf("reserve accepted feedback = %#v auto_run=%v err=%v", reserved, autoRun, err)
	}
	if err := queueRepo.AcknowledgeByIDForSession(ctx, identity, reserved); err != nil {
		t.Fatalf("acknowledge accepted feedback: %v", err)
	}

	retry := planCommentQueuedMessage("preview-drained", queued.ID, "fingerprint-preview-drained", nil)
	retry.Metadata["preview_feedback_refs"] = refs
	planSnapshot, previewSnapshot, replay, err := writer.InsertWithTaskFeedback(
		ctx, identity, retry, nil, refs, "fingerprint-preview-drained", false, nil, 10,
	)
	if err != nil || !replay || planSnapshot != nil || previewSnapshot != nil {
		t.Fatalf("drained replay plan=%#v preview=%#v replay=%v err=%v", planSnapshot, previewSnapshot, replay, err)
	}
	if retry.Content != queued.Content {
		t.Fatalf("replayed content = %q, want %q", retry.Content, queued.Content)
	}
	conflict := planCommentQueuedMessage("preview-drained", queued.ID, "different-fingerprint", nil)
	conflict.Metadata["preview_feedback_refs"] = refs
	if _, _, _, err := writer.InsertWithTaskFeedback(
		ctx, identity, conflict, nil, refs, "different-fingerprint", false, nil, 10,
	); !errors.Is(err, messagequeue.ErrQueueIDConflict) {
		t.Fatalf("conflicting drained replay error = %v, want ErrQueueIDConflict", err)
	}
	entries, err := queueRepo.ListBySession(ctx, queued.SessionID)
	if err != nil || len(entries) != 0 {
		t.Fatalf("queue after replay = %#v, err=%v", entries, err)
	}
}

func seedQueuePreviewFeedback(
	t *testing.T,
	ctx context.Context,
	repo *tasksqlite.Repository,
	suffix string,
) *models.TaskPreviewFeedback {
	t.Helper()
	item := &models.TaskPreviewFeedback{
		ID: "preview-queue-" + suffix, TaskID: "task-" + suffix,
		Kind: models.TaskPreviewFeedbackText, Comment: "Runtime queue copy",
		SourceKind: models.TaskPreviewFeedbackBrowser, SourceLabel: "Local app",
		PageRoute: "/queue", SelectedText: "Generated",
		TextAnchor: json.RawMessage(`{"start":{"node_path":[0],"offset":0},"end":{"node_path":[0],"offset":9},"containing_element":{"tag":"span","outer_html":"<span>Generated</span>"}}`),
	}
	if _, err := repo.CreateTaskPreviewFeedback(ctx, item, "", ""); err != nil {
		t.Fatal(err)
	}
	return item
}
