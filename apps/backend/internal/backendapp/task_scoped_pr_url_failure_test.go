package backendapp

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/github"
	"github.com/kandev/kandev/internal/task/models"
	ws "github.com/kandev/kandev/pkg/websocket"
	"github.com/stretchr/testify/require"
)

type scopedBackendComparisonProbe struct{ calls int }

func (p *scopedBackendComparisonProbe) ReconcileComparisonTarget(context.Context, string, models.ComparisonTargetCandidate) (*models.ComparisonTargetReconciliation, error) {
	p.calls++
	return nil, nil
}
func (p *scopedBackendComparisonProbe) ReconcileComparisonTargetFromSync(ctx context.Context, id string, c models.ComparisonTargetCandidate) (*models.ComparisonTargetReconciliation, error) {
	return p.ReconcileComparisonTarget(ctx, id, c)
}
func (p *scopedBackendComparisonProbe) RemoveComparisonTargetForChange(context.Context, string, string, string, string, int) error {
	p.calls++
	return nil
}

func TestScopedPRURLFullPathFailuresPreserveState(t *testing.T) {
	for _, mode := range []string{"slot collision", "stored stale head", "other active task", "provider failure", "head mismatch"} {
		t.Run(mode, func(t *testing.T) {
			f := newScopedBackendFixture(t)
			probe := &scopedBackendComparisonProbe{}
			f.svc.SetComparisonTargetObserver(probe)
			_, err := f.database.Exec(`UPDATE task_repositories SET base_branch='comparison-sentinel' WHERE task_id='child'`)
			require.NoError(t, err)
			enabled := true
			beforeOptions, err := f.store.UpdateTaskPRAutomationOptions(f.ctx, "child", "fork", 42, github.TaskPRAutomationOptionsPatch{AutoFixEnabled: &enabled, AutoMergeEnabled: &enabled}, false)
			require.NoError(t, err)
			incumbent := &github.TaskPR{ID: "incumbent", WorkspaceID: "ws", TaskID: "child", RepositoryID: "fork", PRNumber: 42, Owner: "upstream", Repo: "project", PRURL: "https://github.com/upstream/project/pull/42", HeadSHA: strings.Repeat("a", 40), State: "open", CreatedAt: time.Now().UTC()}
			switch mode {
			case "slot collision":
				incumbent.Owner = "second"
				incumbent.PRURL = "https://github.com/second/project/pull/42"
				require.NoError(t, f.store.CreateTaskPR(f.ctx, incumbent))
			case "stored stale head":
				incumbent.HeadSHA = strings.Repeat("b", 40)
				require.NoError(t, f.store.CreateTaskPR(f.ctx, incumbent))
			case "other active task":
				_, err := f.database.Exec(`INSERT INTO tasks(id,workspace_id,title,created_at,updated_at) VALUES ('other','ws','Other',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
				require.NoError(t, err)
				incumbent.TaskID = "other"
				require.NoError(t, f.store.CreateTaskPR(f.ctx, incumbent))
			case "provider failure":
				f.client.SetPRDetail("upstream", "project", 42, nil, sql.ErrConnDone)
			case "head mismatch":
				var args map[string]interface{}
				require.NoError(t, json.Unmarshal(f.msg.Payload, &args))
				args["expected_head_sha"] = strings.Repeat("b", 40)
				f.msg.Payload, err = json.Marshal(args)
				require.NoError(t, err)
			}
			response, err := f.dispatcher.Dispatch(f.ctx, f.msg)
			require.NoError(t, err)
			require.Equal(t, ws.MessageTypeError, response.Type, string(response.Payload))
			var payload ws.ErrorPayload
			require.NoError(t, json.Unmarshal(response.Payload, &payload))
			require.Equal(t, true, payload.Details["state_known"])
			require.NotContains(t, payload.Details, "resolved")
			afterOptions, err := f.store.GetTaskPRAutomationOptions(f.ctx, "child", "fork", 42)
			require.NoError(t, err)
			require.Equal(t, beforeOptions, afterOptions)
			var base string
			require.NoError(t, f.database.Get(&base, `SELECT base_branch FROM task_repositories WHERE task_id='child'`))
			require.Equal(t, "comparison-sentinel", base)
			require.Zero(t, probe.calls)
			if mode == "slot collision" || mode == "stored stale head" || mode == "other active task" {
				saved, err := f.store.GetTaskPRByID(f.ctx, incumbent.ID)
				require.NoError(t, err)
				require.Equal(t, incumbent.PRURL, saved.PRURL)
				require.Equal(t, incumbent.HeadSHA, saved.HeadSHA)
			} else {
				rows, err := f.store.ListTaskPRsByTask(f.ctx, "child")
				require.NoError(t, err)
				require.Empty(t, rows)
			}
		})
	}
}
