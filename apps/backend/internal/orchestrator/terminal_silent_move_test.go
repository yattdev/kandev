package orchestrator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/task/models"
	wfmodels "github.com/kandev/kandev/internal/workflow/models"
	workflowmove "github.com/kandev/kandev/internal/workflow/move"
	v1 "github.com/kandev/kandev/pkg/api/v1"
)

func TestSilentTerminalMoveWithoutRetentionDoesNotCreateSessionOrReopenTask(t *testing.T) {
	ctx := context.Background()
	fixture := newProfileSwitchFixture(t, models.WorkflowProfileSessionStartPolicyNew, models.WorkflowProfileSessionEndPolicyPark)
	source := &wfmodels.WorkflowStep{ID: "step-a", WorkflowID: "wf1", Position: 0, AgentProfileID: "profile-a"}
	done := &wfmodels.WorkflowStep{
		ID: "step-b", WorkflowID: "wf1", Name: "Done", Position: 1,
		AgentProfileID: "profile-b", ProfileSessionStartPolicy: models.WorkflowProfileSessionStartPolicyNew,
		CompleteTaskOnEnter: true,
	}
	fixture.stepGetter.steps[source.ID] = source
	fixture.stepGetter.steps[done.ID] = done
	task, err := fixture.repo.GetTask(ctx, "t1")
	require.NoError(t, err)
	task.WorkflowStepID = done.ID
	task.State = v1.TaskStateCompleted
	require.NoError(t, fixture.repo.UpdateTask(ctx, task))
	session, err := fixture.repo.GetTaskSession(ctx, fixture.current.ID)
	require.NoError(t, err)
	session.State = models.TaskSessionStateWaitingForInput
	require.NoError(t, fixture.repo.UpdateTaskSession(ctx, session))
	before, err := fixture.repo.ListTaskSessions(ctx, task.ID)
	require.NoError(t, err)

	entry := workflowmove.OverlayStep(done, &workflowmove.EntryOptions{SkipStepPrompt: true})
	require.NoError(t, fixture.svc.processStepExitAndEnterWithSteps(
		ctx, task.ID, session, source, entry, source.ID, done.ID, task.Description, false, 0,
	))

	after, err := fixture.repo.ListTaskSessions(ctx, task.ID)
	require.NoError(t, err)
	require.Len(t, after, len(before), "silent Done entry must not prepare an idle session")
	stored, err := fixture.repo.GetTask(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, v1.TaskStateCompleted, stored.State)
}
