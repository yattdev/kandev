package provideraccess

import (
	"strings"
	"testing"

	"github.com/kandev/kandev/internal/github"
)

func validGitHubRerunTarget() GitHubRerunTarget {
	return GitHubRerunTarget{
		Operation: "rerun_failed_jobs", PRNumber: 3165,
		BaseRepositoryID: 10, BaseRepository: "kdlbs/kandev", BaseRef: "main",
		BaseSHA:          strings.Repeat("a", 40),
		HeadRepositoryID: 20, HeadRepository: "yattdev/kandev", HeadRef: "feature/provider-access",
		HeadSHA:     strings.Repeat("b", 40),
		SourceRunID: 1234, SourceAttempt: 2, WorkflowID: 55,
	}
}

func TestGitHubRerunTargetDigestBindsForkHeadAndRun(t *testing.T) {
	base := validGitHubRerunTarget()
	digest, err := base.Digest()
	if err != nil || len(digest) != 64 {
		t.Fatalf("base digest = %q, err = %v", digest, err)
	}
	if replay, err := base.Digest(); err != nil || replay != digest {
		t.Fatalf("stable digest = %q, err = %v", replay, err)
	}
	mutations := map[string]func(*GitHubRerunTarget){
		"PR":                 func(v *GitHubRerunTarget) { v.PRNumber++ },
		"base repository ID": func(v *GitHubRerunTarget) { v.BaseRepositoryID++ },
		"base repository":    func(v *GitHubRerunTarget) { v.BaseRepository = "other/kandev" },
		"base ref":           func(v *GitHubRerunTarget) { v.BaseRef = "release" },
		"base SHA":           func(v *GitHubRerunTarget) { v.BaseSHA = strings.Repeat("c", 40) },
		"head repository ID": func(v *GitHubRerunTarget) { v.HeadRepositoryID++ },
		"head repository":    func(v *GitHubRerunTarget) { v.HeadRepository = "other/kandev" },
		"head ref":           func(v *GitHubRerunTarget) { v.HeadRef = "other" },
		"head SHA":           func(v *GitHubRerunTarget) { v.HeadSHA = strings.Repeat("c", 40) },
		"source run":         func(v *GitHubRerunTarget) { v.SourceRunID++ },
		"source attempt":     func(v *GitHubRerunTarget) { v.SourceAttempt++ },
		"workflow":           func(v *GitHubRerunTarget) { v.WorkflowID++ },
	}
	for name, change := range mutations {
		t.Run(name, func(t *testing.T) {
			other := base
			change(&other)
			got, err := other.Digest()
			if err != nil || got == digest {
				t.Fatalf("changed %s digest = %q, err = %v", name, got, err)
			}
		})
	}
}

func TestGitHubRerunTargetDigestRejectsIncompleteIdentity(t *testing.T) {
	for _, mutate := range []func(*GitHubRerunTarget){
		func(v *GitHubRerunTarget) { v.Operation = "dispatch" },
		func(v *GitHubRerunTarget) { v.PRNumber = 0 },
		func(v *GitHubRerunTarget) { v.HeadSHA = "mutable-ref" },
		func(v *GitHubRerunTarget) { v.SourceAttempt = 0 },
		func(v *GitHubRerunTarget) { v.HeadRepository = "" },
	} {
		target := validGitHubRerunTarget()
		mutate(&target)
		if digest, err := target.Digest(); err == nil || digest != "" {
			t.Fatalf("incomplete target digest = %q, err = %v", digest, err)
		}
	}
}

func TestGitHubRerunTargetMatchesCurrentProviderEvidence(t *testing.T) {
	target := validGitHubRerunTarget()
	pr := &github.PR{Number: target.PRNumber, State: "open",
		BaseRepoID: target.BaseRepositoryID, BaseRepoOwner: "kdlbs", BaseRepoName: "kandev",
		BaseBranch: target.BaseRef, BaseSHA: target.BaseSHA,
		HeadRepoID: target.HeadRepositoryID, HeadRepoOwner: "yattdev", HeadRepoName: "kandev",
		HeadBranch: target.HeadRef, HeadSHA: target.HeadSHA}
	run := github.WorkflowRun{ID: target.SourceRunID, RunAttempt: target.SourceAttempt,
		WorkflowID: target.WorkflowID, Event: "pull_request", Status: "completed", Conclusion: "failure",
		HeadSHA: target.HeadSHA, HeadBranch: target.HeadRef, HeadRepoID: target.HeadRepositoryID,
		HeadRepoOwner: "yattdev", HeadRepoName: "kandev"}
	if err := target.MatchProviderEvidence(pr, &run); err != nil {
		t.Fatalf("matching evidence: %v", err)
	}
	cases := map[string]func(*github.PR, *github.WorkflowRun){
		"closed PR":      func(p *github.PR, _ *github.WorkflowRun) { p.State = "closed" },
		"base drift":     func(p *github.PR, _ *github.WorkflowRun) { p.BaseSHA = strings.Repeat("c", 40) },
		"fork swap":      func(p *github.PR, _ *github.WorkflowRun) { p.HeadRepoID++ },
		"head drift":     func(p *github.PR, _ *github.WorkflowRun) { p.HeadSHA = strings.Repeat("c", 40) },
		"run attempt":    func(_ *github.PR, r *github.WorkflowRun) { r.RunAttempt++ },
		"workflow":       func(_ *github.PR, r *github.WorkflowRun) { r.WorkflowID++ },
		"run source":     func(_ *github.PR, r *github.WorkflowRun) { r.Event = "workflow_dispatch" },
		"pending run":    func(_ *github.PR, r *github.WorkflowRun) { r.Status = "in_progress" },
		"successful run": func(_ *github.PR, r *github.WorkflowRun) { r.Conclusion = "success" },
		"run fork swap":  func(_ *github.PR, r *github.WorkflowRun) { r.HeadRepoID++ },
		"run head drift": func(_ *github.PR, r *github.WorkflowRun) { r.HeadSHA = strings.Repeat("c", 40) },
		"foreign association": func(_ *github.PR, r *github.WorkflowRun) {
			r.PullRequests = []github.WorkflowRunPullRequest{{Number: target.PRNumber + 1}}
		},
		"association fork swap": func(_ *github.PR, r *github.WorkflowRun) {
			r.PullRequests = []github.WorkflowRunPullRequest{{Number: target.PRNumber, HeadRepoID: target.HeadRepositoryID + 1}}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			changedPR, changedRun := *pr, run
			mutate(&changedPR, &changedRun)
			if err := target.MatchProviderEvidence(&changedPR, &changedRun); err == nil {
				t.Fatal("changed provider evidence was accepted")
			}
		})
	}
}
