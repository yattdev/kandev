package backendapp

import (
	taskmodels "github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/taskpreview"
	"testing"
)

func TestTaskPreviewRetainedEnvironmentOwnership(t *testing.T) {
	g := taskpreview.Grant{TaskID: "task", EnvironmentID: "environment", TaskRoot: "/data/tasks/owned"}
	environment := &taskmodels.TaskEnvironment{ID: g.EnvironmentID, TaskID: g.TaskID, WorkspacePath: g.TaskRoot}
	if !previewEnvironmentMatches(environment, g) {
		t.Fatal("registered task root denied")
	}
	environment.Status = taskmodels.TaskEnvironmentStatus("parked")
	if !previewEnvironmentMatches(environment, g) {
		t.Fatal("parking invalidated ownership")
	}
	for _, mutate := range []func(*taskmodels.TaskEnvironment){
		func(e *taskmodels.TaskEnvironment) { e.ID = "replacement" },
		func(e *taskmodels.TaskEnvironment) { e.TaskID = "other-task" },
		func(e *taskmodels.TaskEnvironment) { e.WorkspacePath = "/data/tasks/other" },
	} {
		copy := *environment
		mutate(&copy)
		if previewEnvironmentMatches(&copy, g) {
			t.Fatal("substituted ownership accepted")
		}
	}
	if previewEnvironmentMatches(nil, g) {
		t.Fatal("missing ownership accepted")
	}
	environment.WorkspacePath = ""
	environment.TaskDirName = "owned"
	if !previewEnvironmentMatches(environment, g) {
		t.Fatal("legacy retained task-directory identity denied")
	}
}
