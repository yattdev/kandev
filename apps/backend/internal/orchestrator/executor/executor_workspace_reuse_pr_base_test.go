package executor

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	v1 "github.com/kandev/kandev/pkg/api/v1"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceReusePRBaseLaunchPreservesCheckout(t *testing.T) {
	ctx := context.Background()
	repo := newMockRepository()
	repo.repositories["repo-1"] = &models.Repository{
		ID: "repo-1", WorkspaceID: "workspace-1", SourceType: sourceTypeLocal, LocalPath: t.TempDir(),
		Provider: "github", ProviderOwner: "fork-owner", ProviderName: "widgets", DefaultBranch: "main",
	}
	repo.executors["exec-worktree"] = &models.Executor{ID: "exec-worktree", Type: models.ExecutorTypeWorktree}
	tr := &models.TaskRepository{
		ID: "tr-1", TaskID: "task-1", RepositoryID: "repo-1", BaseBranch: "upstream/main",
		CheckoutBranch: "feature", Metadata: map[string]interface{}{"pr_number": 42},
	}
	repo.taskRepositories[tr.ID] = tr
	e := newTestExecutor(t, &mockAgentManager{}, repo)
	target := forkPRComparisonTarget()
	target.TargetBranch = "main"
	e.SetPRBaseResolver(&recordingPRBaseResolver{result: &models.PRBase{
		Target: *target, OID: "0123456789abcdef0123456789abcdef01234567",
	}})
	info, err := e.resolveTaskRepoInfo(ctx, tr)
	require.NoError(t, err)
	require.Equal(t, "main", info.BaseBranch, "fresh checkout uses the verified live PR")
	task := &v1.Task{ID: tr.TaskID, WorkspaceID: "workspace-1"}
	session := &models.TaskSession{ID: "destination", TaskID: task.ID, TaskEnvironmentID: "env-1"}
	env := &models.TaskEnvironment{ID: "env-1", TaskID: task.ID, ExecutorType: string(models.ExecutorTypeWorktree),
		Status: models.TaskEnvironmentStatusReady}
	req, _, err := e.buildLaunchAgentRequest(ctx, task, session, "profile-1", "exec-worktree", "",
		info, []*repoInfo{info}, true, env)
	require.NoError(t, err)
	require.True(t, req.WorkspaceReuseRequired)
	require.Equal(t, "upstream/main", req.BaseBranch, "attach preserves the checkout base")
	require.Nil(t, req.QualifiedPRBase, "qualified PR base applies only to materialization")
	require.Equal(t, target.TargetBranch, req.ComparisonTarget.TargetBranch, "comparison stays live")
	require.Equal(t, "main", info.BaseBranch, "reuse cannot mutate fresh launch configuration")
}

func TestWorkspaceReusePRBaseSlotsPreserveLiveComparisonAndFreshInputs(t *testing.T) {
	target := forkPRComparisonTarget()
	base := &models.PRBase{Target: *target, OID: "0123456789abcdef0123456789abcdef01234567"}
	original := []*repoInfo{
		{TaskRepositoryID: "slot-a", RepositoryID: "repo-1", BaseBranch: "main", WorkspaceBaseBranch: "upstream/main",
			QualifiedPRBase: base, PRBase: base, ComparisonTarget: target, RemoteContribution: &models.RemoteContribution{BaseBranch: "main"}},
		{TaskRepositoryID: "slot-b", RepositoryID: "repo-1", BaseBranch: "stable", WorkspaceBaseBranch: "release", CheckoutBranch: "feature"},
	}
	reused := repoInfosForWorkspaceReuse(original)
	specs := buildRepoSpecs(reused)
	require.Equal(t, "upstream/main", specs[0].BaseBranch)
	require.Equal(t, "release", specs[1].BaseBranch)
	require.Equal(t, "slot-a", specs[0].TaskRepositoryID)
	require.Equal(t, "slot-b", specs[1].TaskRepositoryID)
	require.Nil(t, specs[0].QualifiedPRBase)
	require.Nil(t, specs[0].RemoteContribution)
	require.Equal(t, target, specs[0].ComparisonTarget)
	fresh := buildRepoSpecs(original)
	require.Equal(t, "main", fresh[0].BaseBranch)
	require.Equal(t, base, fresh[0].QualifiedPRBase)
	require.NotNil(t, fresh[0].RemoteContribution)
	require.Equal(t, base, reused[0].PRBase)
}

func TestWorkspaceReusePRBaseResumeRetainsContributionCredentialIdentity(t *testing.T) {
	ctx := context.Background()
	repo := newMockRepository()
	repo.tasks["task-1"] = &models.Task{ID: "task-1"}
	repo.repositories["repo-1"] = &models.Repository{ID: "repo-1", WorkspaceID: "workspace-1",
		SourceType: sourceTypeLocal, LocalPath: t.TempDir(), Provider: "github", ProviderOwner: "upstream",
		ProviderName: "widgets", DefaultBranch: "main"}
	binding := &models.RemoteContribution{
		Version: models.RemoteContributionVersion, Provider: models.RemoteContributionProviderGitHub,
		Kind: models.RemoteContributionKindPullRequest, CanonicalURL: "https://github.com/upstream/widgets/pull/42",
		Number: 42, State: models.RemoteContributionStateOpen, BaseBranch: "main", HeadBranch: "feature",
		HeadSHA: "0123456789abcdef0123456789abcdef01234567", CollaborationAllowed: true,
		SourceRepository: models.RemoteContributionRepository{Host: "github.com", Path: "fork-owner/widgets",
			RemoteURL: "https://github.com/fork-owner/widgets.git"},
	}
	metadata := map[string]interface{}{}
	require.NoError(t, models.PutRemoteContribution(metadata, binding))
	repo.taskRepositories["tr-1"] = &models.TaskRepository{ID: "tr-1", TaskID: "task-1", RepositoryID: "repo-1",
		BaseBranch: "main", CheckoutBranch: "feature", Metadata: metadata}
	env := &models.TaskEnvironment{ID: "env-1", TaskID: "task-1", ExecutorType: string(models.ExecutorTypeLocal),
		MaterializationSessionID: "source", Status: models.TaskEnvironmentStatusReady}
	repo.taskEnvironments[env.ID] = env
	repo.taskEnvironmentRepos[env.ID] = []*models.TaskEnvironmentRepo{{RepositoryID: "repo-1", Status: taskEnvironmentRepoStatusActive}}
	env.Repos = repo.taskEnvironmentRepos[env.ID]
	e := newTestExecutor(t, &mockAgentManager{}, repo)
	session := &models.TaskSession{ID: "destination", TaskID: "task-1", TaskEnvironmentID: env.ID}
	req := &LaunchAgentRequest{TaskID: "task-1", SessionID: session.ID, ExecutorType: string(models.ExecutorTypeLocal)}
	_, _, authRepos, err := e.prepareResumeRepositorySettings(ctx, &v1.Task{ID: "task-1"}, session, req, ResumeOptions{})
	require.NoError(t, err)
	require.True(t, req.WorkspaceReuseRequired)
	require.Len(t, authRepos, 1)
	require.NotNil(t, authRepos[0].RemoteContribution, "resume credential broker still needs the verified fork identity")
	require.Equal(t, "fork-owner/widgets", authRepos[0].RemoteContribution.SourceRepository.Path)
	require.Nil(t, req.RemoteContribution, "attach must not execute contribution materialization")
}
