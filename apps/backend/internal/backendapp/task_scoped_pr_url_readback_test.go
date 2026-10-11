package backendapp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kandev/kandev/internal/github"
	ws "github.com/kandev/kandev/pkg/websocket"
	"github.com/stretchr/testify/require"
)

type scopedReadbackProvider struct {
	*github.Service
	beforeList func()
}

func (p *scopedReadbackProvider) ListTaskPRs(ctx context.Context, taskIDs []string) (map[string][]*github.TaskPR, error) {
	if p.beforeList != nil {
		mutate := p.beforeList
		p.beforeList = nil
		mutate()
	}
	return p.Service.ListTaskPRs(ctx, taskIDs)
}
func TestScopedPRURLFullPathRejectsChangedReadback(t *testing.T) {
	for _, mode := range []string{"detached", "replaced"} {
		t.Run(mode, func(t *testing.T) {
			f := newScopedBackendFixture(t)
			wrapper := &scopedReadbackProvider{Service: f.svc, beforeList: func() {
				row, err := f.store.GetTaskPRByRepoAndNumber(f.ctx, "child", "fork", 42)
				require.NoError(t, err)
				require.NotNil(t, row)
				if mode == "detached" {
					_, _, err = f.store.DetachTaskPR(f.ctx, row.ID)
					require.NoError(t, err)
				} else {
					row.ID = "manual-replacement"
					row.Owner = "second"
					row.PRURL = "https://github.com/second/project/pull/42"
					row.HeadSHA = strings.Repeat("b", 40)
					_, err = f.store.ReplaceTaskPR(f.ctx, row, &github.PRStatus{PR: &github.PR{Number: 42, RepoOwner: row.Owner, RepoName: row.Repo, HTMLURL: row.PRURL, HeadSHA: row.HeadSHA, HeadBranch: row.HeadBranch, BaseBranch: row.BaseBranch, State: row.State}})
					require.NoError(t, err)
				}
			}}
			f.coordinator.github = wrapper
			f.handler.SetTaskChangeLinkService(f.coordinator)
			response, err := f.dispatcher.Dispatch(f.ctx, f.msg)
			require.NoError(t, err)
			require.Equal(t, ws.MessageTypeError, response.Type, string(response.Payload))
			var payload ws.ErrorPayload
			require.NoError(t, json.Unmarshal(response.Payload, &payload))
			require.Equal(t, false, payload.Details["state_known"])
			require.NotContains(t, payload.Details, "resolved")
			rows, err := f.store.ListTaskPRsByTask(f.ctx, "child")
			require.NoError(t, err)
			if mode == "detached" {
				require.Empty(t, rows)
			} else {
				require.Len(t, rows, 1)
				require.Equal(t, "https://github.com/second/project/pull/42", rows[0].PRURL)
			}
		})
	}
}
