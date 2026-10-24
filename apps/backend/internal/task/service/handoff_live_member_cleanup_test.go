package service

import (
	"context"
	"errors"
	"testing"
	"time"

	orchmodels "github.com/kandev/kandev/internal/office/models"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository"
)

type repairableCascadeGroupRepo struct{ *fakeWSGroupRepoCascade }

func (r *repairableCascadeGroupRepo) ListWorkspaceGroupMembers(
	_ context.Context, groupID string,
) ([]orchmodels.WorkspaceGroupMember, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var members []orchmodels.WorkspaceGroupMember
	for taskID, role := range r.allMembers[groupID] {
		member := orchmodels.WorkspaceGroupMember{
			WorkspaceGroupID: groupID, TaskID: taskID, Role: role,
		}
		for _, release := range r.releaseCalls {
			if release.groupID == groupID && release.taskID == taskID {
				if _, active := r.members[groupID][taskID]; !active {
					now := time.Now().UTC()
					member.ReleasedAt = &now
					member.ReleasedByCascadeID = release.cascadeID
				}
				break
			}
		}
		members = append(members, member)
	}
	return members, nil
}

func (r *repairableCascadeGroupRepo) RestoreWorkspaceGroupMemberByCascade(
	ctx context.Context, taskID, cascadeID string,
) error {
	if err := r.fakeWSGroupRepoCascade.RestoreWorkspaceGroupMemberByCascade(ctx, taskID, cascadeID); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for groupID, historical := range r.allMembers {
		if role, ok := historical[taskID]; ok {
			r.members[groupID][taskID] = role
		}
	}
	return nil
}

// A committed child deletion can lose its response while its live parent's
// membership restoration fails. The group remains an executable repair
// obligation; neither the first request nor a later evaluator may clean it.
func TestPartialDeleteRestoreFailureProtectsLiveWorkspace(t *testing.T) {
	ctx := context.Background()
	svc, _, repo := createTestService(t)
	svc.StopTaskResourceCleanupWorker()
	seedCleanupTaskAndSession(t, repo, "live-root", "live-root-session")
	if err := repo.CreateTask(ctx, &models.Task{
		ID: "live-child", ParentID: "live-root", WorkspaceID: "ws-live-root",
		WorkflowID: "wf-live-root", WorkflowStepID: "step", Title: "Child",
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateTaskSession(ctx, &models.TaskSession{
		ID: "live-child-session", TaskID: "live-child", State: models.TaskSessionStateCompleted,
	}); err != nil {
		t.Fatal(err)
	}
	groups := &repairableCascadeGroupRepo{newCascadeWSGroupRepo()}
	groups.groups["g1"] = &orchmodels.WorkspaceGroup{
		ID: "g1", WorkspaceID: "ws-live-root", OwnerTaskID: "live-root",
		MaterializedKind: orchmodels.WorkspaceGroupKindPlainFolder,
		MaterializedPath: "/managed/live-root", OwnedByKandev: true,
		CleanupPolicy: orchmodels.WorkspaceCleanupPolicyDeleteWhenLastMemberArchivedOrDel,
		CleanupStatus: orchmodels.WorkspaceCleanupStatusActive,
	}
	groups.members["g1"] = map[string]string{
		"live-root":  orchmodels.WorkspaceMemberRoleOwner,
		"live-child": orchmodels.WorkspaceMemberRoleMember,
	}
	groups.allMembers["g1"] = map[string]string{
		"live-root":  orchmodels.WorkspaceMemberRoleOwner,
		"live-child": orchmodels.WorkspaceMemberRoleMember,
	}
	restoreErr := errors.New("membership restoration unavailable")
	groups.restoreErr = restoreErr
	var taskRepo repository.TaskRepository = &cascadeCommitResponseLost{repo}
	handoff := NewHandoffService(taskRepo, nil, nil, nil, groups, nil)
	handoff.SetTaskResourceCleaner(svc)
	handoff.SetSessionReader(repo)
	cleaner := &fakeWorkspaceCleaner{}
	handoff.SetWorkspaceCleaner(cleaner)
	out, err := handoff.DeleteTaskTree(ctx, "live-root", true)
	if !errors.Is(err, restoreErr) {
		t.Fatalf("DeleteTaskTree error = %v, want restoration failure", err)
	}
	root, getErr := repo.GetTask(ctx, "live-root")
	if getErr != nil || root == nil || root.ArchivedAt != nil {
		t.Fatalf("live root after partial delete = %+v, err=%v", root, getErr)
	}
	if out == nil || len(out.ArchivedTaskIDs) != 1 || out.ArchivedTaskIDs[0] != "live-child" {
		t.Fatalf("partial delete outcome = %+v", out)
	}
	if len(cleaner.plainFolders) != 0 || groups.cleanupStatuses["g1"] != "" {
		t.Fatalf("unsafe cleanup: cleaner=%v status=%q", cleaner.plainFolders, groups.cleanupStatuses["g1"])
	}
	// A later evaluator sees the persisted released-by-cascade row. Failed
	// repair must still deny cleanup; a successful repair reopens membership
	// without cleaning a live parent's workspace.
	if err := handoff.evaluateWorkspaceGroupCleanup(ctx, "g1"); !errors.Is(err, restoreErr) {
		t.Fatalf("retry with unavailable repair = %v, want restoration failure", err)
	}
	if len(cleaner.plainFolders) != 0 || groups.cleanupStatuses["g1"] != "" {
		t.Fatalf("retry cleaned live workspace: cleaner=%v status=%q", cleaner.plainFolders, groups.cleanupStatuses["g1"])
	}
	groups.restoreErr = nil
	if err := handoff.evaluateWorkspaceGroupCleanup(ctx, "g1"); err != nil {
		t.Fatalf("membership repair: %v", err)
	}
	if len(cleaner.plainFolders) != 0 || groups.cleanupStatuses["g1"] != "" {
		t.Fatalf("successful repair cleaned live workspace: cleaner=%v status=%q", cleaner.plainFolders, groups.cleanupStatuses["g1"])
	}
}
