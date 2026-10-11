package backendapp

import (
	"context"
	"errors"

	"github.com/kandev/kandev/internal/auth/authn"
	"github.com/kandev/kandev/internal/github"
	mcp "github.com/kandev/kandev/internal/mcp/handlers"
)

type scopedPRURLProvider interface {
	AssociateScopedPRURL(context.Context, github.ScopedPRURLRequest) (*github.TaskPR, error)
}

func (c taskChangeLinkCoordinator) scopedPRURLMutation(ctx context.Context, req mcp.TaskChangeLinkRequest) (mcp.TaskChangeLinkMutationResult, error) {
	result := mcp.TaskChangeLinkMutationResult{TaskID: req.TaskID}
	if req.Operation != "link" || req.Link.Provider != "github" || req.ExpectedHeadSHA == "" {
		return c.scopedPRURLFailure(ctx, result, errors.New("invalid scoped PR operation"))
	}
	task, repo, err := c.taskRepository(ctx, req.TaskID, req.Link.RepositoryID)
	if err != nil {
		return c.scopedPRURLFailure(ctx, result, err)
	}
	provider, ok := c.github.(scopedPRURLProvider)
	if !ok || repo.Provider != "github" {
		return c.scopedPRURLFailure(ctx, result, errors.New("scoped PR association unavailable"))
	}
	identity, ok := authn.IdentityFromContext(ctx)
	if !ok && c.singleUserIdentity != nil {
		identity, ok = c.singleUserIdentity()
	}
	if !ok || identity.UserID == "" {
		return c.scopedPRURLFailure(ctx, result, errors.New("authenticated identity required"))
	}
	row, err := provider.AssociateScopedPRURL(ctx, github.ScopedPRURLRequest{WorkspaceID: task.WorkspaceID, UserID: identity.UserID, TaskID: task.ID, RepositoryID: repo.ID, URL: req.PRURL, ExpectedHead: req.ExpectedHeadSHA, SourceOwner: repo.ProviderOwner, SourceRepo: repo.ProviderName, Number: req.Link.Number})
	if err != nil || row == nil {
		return c.scopedPRURLFailure(ctx, result, err)
	}
	result.Links, err = c.list(ctx, task.ID)
	if err != nil {
		result.OperationError = taskChangeMutationReadbackError
		return result, err
	}
	result.StateKnown = true
	result.Resolved = &mcp.TaskChangeURLAssociation{RepositoryID: row.RepositoryID, Number: row.PRNumber, URL: row.PRURL, HeadSHA: row.HeadSHA}
	return result, nil
}
func (c taskChangeLinkCoordinator) scopedPRURLFailure(ctx context.Context, result mcp.TaskChangeLinkMutationResult, err error) (mcp.TaskChangeLinkMutationResult, error) {
	if err == nil {
		err = errors.New("scoped PR association readback failed")
	}
	c.logMutationError(result.TaskID, "scoped URL", err)
	if errors.Is(err, github.ErrScopedPRURLReadback) {
		result.OperationError = taskChangeMutationReadbackError
		return result, err
	}
	links, readErr := c.list(ctx, result.TaskID)
	result.OperationError = taskChangeMutationOperationError
	result.Links = links
	result.StateKnown = readErr == nil
	return result, err
}
