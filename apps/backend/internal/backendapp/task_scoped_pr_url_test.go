package backendapp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kandev/kandev/internal/auth/authn"
	"github.com/kandev/kandev/internal/github"
	mcp "github.com/kandev/kandev/internal/mcp/handlers"
	"github.com/stretchr/testify/require"
)

type scopedURLProvider struct {
	fakeGitHubChangeLinks
	request github.ScopedPRURLRequest
}

func (p *scopedURLProvider) AssociateScopedPRURL(_ context.Context, req github.ScopedPRURLRequest) (*github.TaskPR, error) {
	p.request = req
	return &github.TaskPR{TaskID: req.TaskID, RepositoryID: req.RepositoryID, PRNumber: req.Number, PRURL: req.URL, HeadSHA: req.ExpectedHead}, nil
}
func TestTaskChangeCoordinatorScopedPRURL(t *testing.T) {
	tasks, repo := newTaskChangeCoordinatorHarness(t)
	seedTaskChangeCoordinatorTask(t, repo, "ws", "child", "fork", "github.com", "fork", "project")
	provider := &scopedURLProvider{}
	c := taskChangeLinkCoordinator{tasks: tasks, github: provider, singleUserIdentity: func() (authn.Identity, bool) { return authn.Identity{UserID: "user"}, true }}
	req := mcp.TaskChangeLinkRequest{Operation: "link", TaskID: "child", Link: mcp.TaskChangeLink{Provider: "github", RepositoryID: "fork", Number: 42}}
	require.NoError(t, json.Unmarshal([]byte(`{"PRURL":"https://github.com/upstream/project/pull/42","ExpectedHeadSHA":"`+strings.Repeat("a", 40)+`"}`), &req))
	_, err := c.ManageTaskChangeRequest(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, "https://github.com/upstream/project/pull/42", provider.request.URL)
	require.Equal(t, "fork", provider.request.SourceOwner)
}
