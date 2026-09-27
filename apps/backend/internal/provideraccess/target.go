package provideraccess

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/kandev/kandev/internal/github"
)

var ErrTargetInvalid = errors.New("provider access target identity invalid")

// GitHubRerunTarget is the immutable provider identity captured by the Host.
// The plugin cannot choose or change this identity when redeeming a lease.
type GitHubRerunTarget struct {
	Operation        string
	PRNumber         int
	BaseRepositoryID int64
	BaseRepository   string
	BaseRef          string
	BaseSHA          string
	HeadRepositoryID int64
	HeadRepository   string
	HeadRef          string
	HeadSHA          string
	SourceRunID      int64
	SourceAttempt    int
	WorkflowID       int64
}

// Digest binds every target selector into the durable lease without storing
// a caller-provided mutable ref as authority.
func (target GitHubRerunTarget) Digest() (string, error) {
	if target.Operation != "rerun_failed_jobs" || target.PRNumber <= 0 ||
		target.BaseRepositoryID <= 0 || !validCanonicalGitHubRepository(target.BaseRepository) ||
		target.BaseRef == "" || !validGitHubSHA(target.BaseSHA) ||
		target.HeadRepositoryID <= 0 || !validCanonicalGitHubRepository(target.HeadRepository) ||
		target.HeadRef == "" || !validGitHubSHA(target.HeadSHA) ||
		target.SourceRunID <= 0 || target.SourceAttempt <= 0 || target.WorkflowID <= 0 {
		return "", ErrTargetInvalid
	}
	encoded, err := json.Marshal(target)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func validGitHubSHA(value string) bool {
	if len(value) != 40 {
		return false
	}
	return strings.IndexFunc(value, func(ch rune) bool {
		return (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f')
	}) == -1
}

// MatchProviderEvidence checks a fresh provider PR and source run against the
// Host-bound target. It never performs the plugin's provider mutation.
func (target GitHubRerunTarget) MatchProviderEvidence(pr *github.PR, run *github.WorkflowRun) error {
	if _, err := target.Digest(); err != nil {
		return err
	}
	if !target.matchesPR(pr) || !target.matchesRun(run) {
		return ErrTargetInvalid
	}
	if len(run.PullRequests) == 0 {
		return nil
	}
	for _, associated := range run.PullRequests {
		if target.matchesRunAssociation(associated) {
			return nil
		}
	}
	return ErrTargetInvalid
}

func (target GitHubRerunTarget) matchesPR(pr *github.PR) bool {
	if pr == nil || pr.State != "open" || pr.Number != target.PRNumber ||
		pr.BaseRepoID != target.BaseRepositoryID || pr.BaseBranch != target.BaseRef ||
		pr.BaseSHA != target.BaseSHA || pr.HeadRepoID != target.HeadRepositoryID ||
		pr.HeadBranch != target.HeadRef || pr.HeadSHA != target.HeadSHA {
		return false
	}
	return strings.EqualFold(pr.BaseRepoOwner+"/"+pr.BaseRepoName, target.BaseRepository) &&
		strings.EqualFold(pr.HeadRepoOwner+"/"+pr.HeadRepoName, target.HeadRepository)
}

func (target GitHubRerunTarget) matchesRun(run *github.WorkflowRun) bool {
	if run == nil || run.ID != target.SourceRunID || run.RunAttempt != target.SourceAttempt ||
		run.WorkflowID != target.WorkflowID || run.Event != "pull_request" ||
		run.Status != "completed" || run.Conclusion != "failure" ||
		run.HeadSHA != target.HeadSHA || run.HeadBranch != target.HeadRef ||
		run.HeadRepoID != target.HeadRepositoryID {
		return false
	}
	return strings.EqualFold(run.HeadRepoOwner+"/"+run.HeadRepoName, target.HeadRepository)
}

func (target GitHubRerunTarget) matchesRunAssociation(associated github.WorkflowRunPullRequest) bool {
	if associated.Number != target.PRNumber ||
		(associated.HeadSHA != "" && associated.HeadSHA != target.HeadSHA) ||
		(associated.HeadBranch != "" && associated.HeadBranch != target.HeadRef) ||
		(associated.HeadRepoID != 0 && associated.HeadRepoID != target.HeadRepositoryID) {
		return false
	}
	if associated.HeadRepoOwner != "" || associated.HeadRepoName != "" {
		return strings.EqualFold(associated.HeadRepoOwner+"/"+associated.HeadRepoName, target.HeadRepository)
	}
	return true
}
