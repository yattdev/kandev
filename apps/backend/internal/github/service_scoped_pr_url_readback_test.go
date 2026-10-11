package github

import (
	"context"
	"strings"
	"testing"

	"github.com/kandev/kandev/internal/events/bus"
	"github.com/stretchr/testify/require"
)

type scopedReadbackBus struct {
	mockEventBus
	afterCommit func()
}

func (b *scopedReadbackBus) Publish(ctx context.Context, topic string, event *bus.Event) error {
	err := b.mockEventBus.Publish(ctx, topic, event)
	if b.afterCommit != nil {
		mutate := b.afterCommit
		b.afterCommit = nil
		mutate()
	}
	return err
}
func TestScopedPRURLReadbackRejectsManualReplacement(t *testing.T) {
	_, svc, client, store := setupPollerTest(t)
	seedScopedPRURLTasks(t, store)
	client.AddRepos("upstream", []GitHubRepo{{Owner: "upstream", Name: "project", FullName: "upstream/project"}})
	client.SetPRDetail("upstream", "project", 42, scopedPRFixture(), nil)
	ctx := scopedPRURLContext()
	req := scopedPRRequest()
	svc.eventBus = &scopedReadbackBus{afterCommit: func() {
		pr := *scopedPRFixture()
		pr.RepoOwner = "second"
		pr.BaseRepoOwner = "second"
		pr.HTMLURL = "https://github.com/second/project/pull/42"
		pr.HeadSHA = strings.Repeat("b", 40)
		replacement := &TaskPR{TaskID: "child", WorkspaceID: "ws-1", RepositoryID: "fork", PRNumber: 42, Owner: pr.RepoOwner, Repo: pr.RepoName, PRURL: pr.HTMLURL, HeadSHA: pr.HeadSHA, State: pr.State}
		_, err := store.ReplaceTaskPR(ctx, replacement, &PRStatus{PR: &pr, OutcomeFieldsPopulated: true})
		require.NoError(t, err)
	}}
	row, err := svc.AssociateScopedPRURL(ctx, req)
	require.ErrorIs(t, err, ErrScopedPRURLReadback)
	require.Nil(t, row)
	persisted, err := store.GetTaskPRByRepoAndNumber(ctx, "child", "fork", 42)
	require.NoError(t, err)
	require.Equal(t, "https://github.com/second/project/pull/42", persisted.PRURL)
}
