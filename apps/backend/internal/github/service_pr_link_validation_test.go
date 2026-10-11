package github

import (
	"context"
	"strings"
	"testing"
	"time"

	taskmodels "github.com/kandev/kandev/internal/task/models"
)

const verifiedLinkURL = "https://github.com/acme/widget/pull/7"

func verifiedLinkPR() *PR {
	mergedAt := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	return &PR{
		Number: 7, State: "merged", HTMLURL: verifiedLinkURL,
		RepoOwner: "acme", RepoName: "widget", BaseRepoOwner: "acme", BaseRepoName: "widget",
		HeadRepoOwner: "contributor", HeadRepoName: "widget-fork",
		HeadBranch: "feature/deliverable", BaseBranch: "main", HeadSHA: strings.Repeat("a", 40),
		CreatedAt: mergedAt.Add(-time.Hour), MergedAt: &mergedAt,
	}
}

func setupVerifiedLink(t *testing.T, pr *PR) (*Service, *Store) {
	t.Helper()
	_, svc, client, store := setupPollerTest(t)
	client.AddRepos("acme", []GitHubRepo{{Owner: "acme", Name: "widget", FullName: "acme/widget"}})
	svc.SetTaskIssueStore(&fakeTaskIssueStore{
		task:  &taskmodels.Task{ID: "child", WorkspaceID: testWorkspaceID},
		repos: []*taskmodels.TaskRepository{{ID: "attachment", RepositoryID: "fork-repo"}},
	})
	// Key the mock by the requested URL even when testing a mismatched response.
	client.reviewWatch.details[prKey{"acme", "widget", 7}] = mockPRDetailResult{pr: pr}
	return svc, store
}

func linkVerifiedPR(svc *Service) (*TaskPR, error) {
	return svc.AssociateVerifiedSourcePRByURLForWorkspace(context.Background(), testWorkspaceID,
		"test-user", "child", "fork-repo", verifiedLinkURL, "contributor", "widget-fork")
}

func TestVerifiedPRLinkMergedDeliveryAndRetry(t *testing.T) {
	pr := verifiedLinkPR()
	svc, store := setupVerifiedLink(t, pr)
	for range 2 {
		got, err := linkVerifiedPR(svc)
		if err != nil {
			t.Fatal(err)
		}
		if got.TaskID != "child" || got.RepositoryID != "fork-repo" || got.PRURL != verifiedLinkURL ||
			got.HeadSHA != pr.HeadSHA || got.State != "merged" || got.MergedAt == nil || !got.MergedAt.Equal(*pr.MergedAt) {
			t.Fatalf("wrong association: %+v", got)
		}
	}
	rows, err := store.ListTaskPRsByTask(context.Background(), "child")
	if err != nil || len(rows) != 1 || rows[0].DetachedAt != nil {
		t.Fatalf("retry must leave exactly one active link: %+v, %v", rows, err)
	}
	watches, err := store.ListPRWatchesByTask(context.Background(), "child")
	if err != nil || len(watches) != 0 {
		t.Fatalf("unexpected watches: %+v, %v", watches, err)
	}
}

func TestVerifiedPRLinkRejectsUnsafeMergedResponses(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*PR)
	}{
		{"wrong number", func(p *PR) { p.Number++ }},
		{"wrong owner", func(p *PR) { p.RepoOwner = "other" }},
		{"wrong base", func(p *PR) { p.BaseRepoName = "other" }},
		{"wrong URL", func(p *PR) { p.HTMLURL = "https://github.com/other/widget/pull/7" }},
		{"wrong source owner", func(p *PR) { p.HeadRepoOwner = "other" }},
		{"wrong source name", func(p *PR) { p.HeadRepoName = "other" }},
		{"missing source", func(p *PR) { p.HeadRepoOwner = "" }},
		{"unsafe head", func(p *PR) { p.HeadBranch = "--upload-pack=evil" }},
		{"unsafe base", func(p *PR) { p.BaseBranch = "../main" }},
		{"missing SHA", func(p *PR) { p.HeadSHA = "" }},
		{"closed unmerged", func(p *PR) { p.State = "closed"; p.MergedAt = nil }},
		{"unknown state", func(p *PR) { p.State = "unknown" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pr := verifiedLinkPR()
			tc.mutate(pr)
			svc, store := setupVerifiedLink(t, pr)
			if _, err := linkVerifiedPR(svc); err == nil {
				t.Fatal("unsafe link accepted")
			}
			rows, err := store.ListTaskPRsByTask(context.Background(), "child")
			if err != nil || len(rows) != 0 {
				t.Fatalf("rejected request wrote links: %+v, %v", rows, err)
			}
		})
	}
}

func TestVerifiedPRLinkPreservesCollisionAndStaleGuards(t *testing.T) {
	for _, mode := range []string{"collision", "stale", "detached collision"} {
		t.Run(mode, func(t *testing.T) {
			pr := verifiedLinkPR()
			svc, store := setupVerifiedLink(t, pr)
			existing := &TaskPR{TaskID: "child", RepositoryID: "fork-repo", PRNumber: 7,
				PRURL: verifiedLinkURL, HeadSHA: pr.HeadSHA, CreatedAt: pr.CreatedAt}
			if mode == "stale" {
				existing.HeadSHA = strings.Repeat("b", 40)
			} else {
				existing.PRURL = "https://github.com/contributor/widget-fork/pull/7"
			}
			if err := store.CreateTaskPR(context.Background(), existing); err != nil {
				t.Fatal(err)
			}
			if mode == "detached collision" {
				if _, _, err := store.DetachTaskPR(context.Background(), existing.ID); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := linkVerifiedPR(svc); err == nil {
				t.Fatal("conflicting link accepted")
			}
			got, err := store.GetTaskPRByRepoAndNumberIncludingDetached(context.Background(), "child", "fork-repo", 7)
			if err != nil || got.PRURL != existing.PRURL || got.HeadSHA != existing.HeadSHA {
				t.Fatalf("existing association changed: %+v, %v", got, err)
			}
		})
	}
}

func TestVerifiedPRLinkDoesNotAdmitMergedContributionEditing(t *testing.T) {
	pr := verifiedLinkPR()
	pr.MaintainerCanModify = true
	if _, err := buildGitHubRemoteContribution(verifiedLinkURL, "acme", "widget", 7, pr); err == nil {
		t.Fatal("merged PR admitted as editable contribution")
	}
	pr.State = "open"
	pr.MergedAt = nil
	svc, _ := setupVerifiedLink(t, pr)
	if _, err := linkVerifiedPR(svc); err != nil {
		t.Fatalf("open linking regressed: %v", err)
	}
}
