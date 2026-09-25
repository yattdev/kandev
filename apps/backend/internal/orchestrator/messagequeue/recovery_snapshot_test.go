package messagequeue

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kandev/kandev/internal/common/logger"
)

func recoveryTestQueue(t *testing.T) *Service {
	t.Helper()
	log, err := logger.NewLogger(logger.LoggingConfig{Level: "error", Format: "console", OutputPath: "stderr"})
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}
	queue := NewServiceMemory(log)
	queue.SetAutoMergeEnabled(false)
	return queue
}

func TestRecoverySnapshotPageReconstructsFIFOWithoutMutation(t *testing.T) {
	ctx := context.Background()
	queue := recoveryTestQueue(t)
	bodies := []string{strings.Repeat("a", 1023) + "é" + strings.Repeat("b", 600), "second body"}
	ids := make([]string, 0, len(bodies))
	for _, body := range bodies {
		entry, err := queue.QueueMessage(ctx, "terminal-session", "task-1", body, "", QueuedByAgent, false,
			[]MessageAttachment{{Type: "file", Name: "excluded.txt", Data: "excluded"}})
		if err != nil {
			t.Fatalf("queue message: %v", err)
		}
		ids = append(ids, entry.ID)
	}

	before, err := queue.repo.ListBySession(ctx, "terminal-session")
	if err != nil {
		t.Fatalf("list before: %v", err)
	}
	var cursor string
	var snapshot string
	reconstructed := map[string]string{}
	var orderedIDs []string
	for {
		page, err := queue.RecoverySnapshotPage(ctx, "terminal-session", cursor, MinRecoveryPageBodyBytes)
		if err != nil {
			t.Fatalf("recovery page: %v", err)
		}
		if page.PageBodyBytes > MinRecoveryPageBodyBytes {
			t.Fatalf("page body bytes = %d", page.PageBodyBytes)
		}
		if snapshot == "" {
			snapshot = page.SnapshotSHA256
		} else if page.SnapshotSHA256 != snapshot {
			t.Fatalf("snapshot changed between pages")
		}
		for _, fragment := range page.Fragments {
			if fragment.AttachmentCount != 1 {
				t.Fatalf("attachment count = %d", fragment.AttachmentCount)
			}
			if _, seen := reconstructed[fragment.ID]; !seen {
				orderedIDs = append(orderedIDs, fragment.ID)
			}
			reconstructed[fragment.ID] += fragment.Body
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	for i, id := range ids {
		if orderedIDs[i] != id || reconstructed[id] != bodies[i] {
			t.Fatalf("entry %d was not reconstructed in FIFO order", i)
		}
	}
	after, err := queue.repo.ListBySession(ctx, "terminal-session")
	if err != nil {
		t.Fatalf("list after: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("queue count changed: %d -> %d", len(before), len(after))
	}
	for i := range before {
		if after[i].ID != before[i].ID || after[i].Content != before[i].Content || after[i].Position != before[i].Position {
			t.Fatalf("queue entry %d mutated", i)
		}
	}
}

func TestRecoverySnapshotPageRejectsChangedSnapshot(t *testing.T) {
	ctx := context.Background()
	queue := recoveryTestQueue(t)
	if _, err := queue.QueueMessage(ctx, "terminal-session", "task-1", strings.Repeat("x", 2048), "", QueuedByAgent, false, nil); err != nil {
		t.Fatalf("queue first: %v", err)
	}
	first, err := queue.RecoverySnapshotPage(ctx, "terminal-session", "", MinRecoveryPageBodyBytes)
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first page = %#v, err = %v", first, err)
	}
	if _, err := queue.QueueMessage(ctx, "terminal-session", "task-1", "changed", "", QueuedByAgent, false, nil); err != nil {
		t.Fatalf("queue second: %v", err)
	}
	_, err = queue.RecoverySnapshotPage(ctx, "terminal-session", first.NextCursor, MinRecoveryPageBodyBytes)
	if !errors.Is(err, ErrQueueRecoveryChanged) {
		t.Fatalf("error = %v, want ErrQueueRecoveryChanged", err)
	}
}

func TestRecoverySnapshotPageRejectsInvalidScopeAndPageSize(t *testing.T) {
	queue := recoveryTestQueue(t)
	for _, testCase := range []struct {
		session string
		cursor  string
		limit   int
	}{
		{session: "", limit: MinRecoveryPageBodyBytes},
		{session: "session-1", cursor: "not-base64", limit: MinRecoveryPageBodyBytes},
		{session: "session-1", limit: MinRecoveryPageBodyBytes - 1},
		{session: "session-1", limit: MaxRecoveryPageBodyBytes + 1},
	} {
		_, err := queue.RecoverySnapshotPage(context.Background(), testCase.session, testCase.cursor, testCase.limit)
		if !errors.Is(err, ErrInvalidQueueRecoveryPage) {
			t.Fatalf("request %#v error = %v", testCase, err)
		}
	}
}
