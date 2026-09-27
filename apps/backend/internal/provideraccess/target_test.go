package provideraccess

import (
	"strings"
	"testing"
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
