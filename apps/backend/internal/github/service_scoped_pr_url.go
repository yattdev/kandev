package github

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/kandev/kandev/internal/common/securityutil"
	"github.com/kandev/kandev/internal/events"
	"github.com/kandev/kandev/internal/events/bus"
	"github.com/kandev/kandev/internal/mcp/scope"
)

var ErrScopedPRURLReadback = errors.New("scoped PR association readback failed")

// ScopedPRURLRequest keeps the attached source distinct from the canonical PR target.
// Caller task/session authority is derived from context, never from this request.
type ScopedPRURLRequest struct {
	WorkspaceID, UserID, TaskID, RepositoryID  string
	URL, ExpectedHead, SourceOwner, SourceRepo string
	Number                                     int
}

func validateScopedPRURL(owner, repo string, number int, pr *PR, sourceOwner, sourceRepo, expectedHead string) error {
	if err := validateScopedPRIdentity(owner, repo, number, pr); err != nil {
		return err
	}
	return validateScopedPRHeadSource(pr, sourceOwner, sourceRepo, expectedHead)
}

func validateScopedPRIdentity(owner, repo string, number int, pr *PR) error {
	if pr == nil || pr.Number != number || !strings.EqualFold(pr.RepoOwner, owner) || !strings.EqualFold(pr.RepoName, repo) || !strings.EqualFold(pr.BaseRepoOwner, owner) || !strings.EqualFold(pr.BaseRepoName, repo) {
		return errors.New("PR target identity is incomplete or changed")
	}
	if pr.State != defaultPRState && pr.State != prStateMerged {
		return errors.New("PR is neither open nor merged")
	}
	resolvedOwner, resolvedRepo, resolvedNumber, err := parseScopedPRURL(pr.HTMLURL)
	if err != nil || !strings.EqualFold(resolvedOwner, owner) || !strings.EqualFold(resolvedRepo, repo) || resolvedNumber != number {
		return errors.New("PR canonical URL changed")
	}
	return nil
}

func validFullPRHead(head string) bool {
	return len(head) == 40 && securityutil.LooksLikeCommitSHA(head)
}

func validateScopedPRHeadSource(pr *PR, sourceOwner, sourceRepo, expectedHead string) error {
	if !securityutil.IsValidBranchName(pr.HeadBranch) || !securityutil.IsValidBaseBranchRef(pr.BaseBranch) || !validFullPRHead(pr.HeadSHA) || !validFullPRHead(expectedHead) || !strings.EqualFold(pr.HeadSHA, expectedHead) {
		return errors.New("PR head is unsafe or stale")
	}
	if !strings.EqualFold(pr.HeadRepoOwner, sourceOwner) || !strings.EqualFold(pr.HeadRepoName, sourceRepo) || !validGitHubPathPart(sourceOwner) || !validGitHubPathPart(sourceRepo) {
		return errors.New("PR source does not match attached repository")
	}
	return nil
}

var scopedPRURLPattern = regexp.MustCompile(`^https://(?i:github[.]com)/[a-zA-Z0-9_-][a-zA-Z0-9_.-]*/[a-zA-Z0-9_-][a-zA-Z0-9_.-]*/pull/[1-9][0-9]*/?$`)

func parseScopedPRURL(raw string) (string, string, int, error) {
	owner, repo, number, err := parseGitHubContributionURL(raw)
	if err != nil || !scopedPRURLPattern.MatchString(raw) {
		return "", "", 0, ErrInvalidRemoteContributionURL
	}
	return owner, repo, number, nil
}

// AssociateScopedPRURL performs a read-only provider resolution followed by a
// guarded transaction. It does not create watches or grant contribution editing.
func (s *Service) AssociateScopedPRURL(ctx context.Context, req ScopedPRURLRequest) (*TaskPR, error) {
	pr, err := s.resolveScopedPRURL(ctx, req)
	if err != nil {
		return nil, err
	}
	row, changed, err := s.store.associateScopedPRURL(ctx, req, pr)
	if err != nil {
		return nil, err
	}
	if changed {
		if s.eventBus != nil {
			_ = s.eventBus.Publish(ctx, events.GitHubTaskPRUpdated, bus.NewEvent(events.GitHubTaskPRUpdated, "github", row))
		}
		s.reconcileComparisonTarget(ctx, req.TaskID, pr)
	}
	persisted, err := s.store.GetTaskPRByRepoAndNumber(ctx, req.TaskID, req.RepositoryID, req.Number)
	if err != nil || persisted == nil {
		return nil, ErrScopedPRURLReadback
	}
	return persisted, nil
}

func (s *Service) resolveScopedPRURL(ctx context.Context, req ScopedPRURLRequest) (*PR, error) {
	owner, repo, number, err := s.validateScopedPRRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	if err = s.ensureRepositoryInWorkspaceScope(ctx, req.WorkspaceID, owner, repo); err != nil {
		return nil, err
	}
	// Revalidate database authority before any provider request and again at commit.
	if err = s.store.checkScopedPRURLAdmission(ctx, req); err != nil {
		return nil, err
	}
	client, err := s.resolvePersonalReadClient(ctx, req.WorkspaceID, req.UserID, owner, repo)
	if err != nil {
		return nil, err
	}
	pr, err := client.Client.GetPR(ctx, owner, repo, number)
	if err != nil {
		return nil, errors.New("PR provider read failed")
	}
	if err = validateScopedPRURL(owner, repo, number, pr, req.SourceOwner, req.SourceRepo, req.ExpectedHead); err != nil {
		return nil, err
	}
	return pr, nil
}

func (s *Service) validateScopedPRRequest(ctx context.Context, req ScopedPRURLRequest) (string, string, int, error) {
	principal, ok := scope.PrincipalFromContext(ctx)
	if !ok || principal.WorkspaceID != req.WorkspaceID || req.UserID == "" {
		return "", "", 0, errors.New("trusted task session required")
	}
	if s.store == nil || len(req.ExpectedHead) != 40 || !securityutil.LooksLikeCommitSHA(req.ExpectedHead) {
		return "", "", 0, errors.New("full expected PR head required")
	}
	owner, repo, number, err := parseScopedPRURL(req.URL)
	if err != nil || number != req.Number {
		return "", "", 0, ErrInvalidRemoteContributionURL
	}
	return owner, repo, number, nil
}
