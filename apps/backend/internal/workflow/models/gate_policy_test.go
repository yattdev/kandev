package models

import (
	"encoding/json"
	taskmodels "github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCompletionGatePolicyPortableRoundTrip(t *testing.T) {
	wf := &taskmodels.Workflow{ID: "wf", Name: "Workflow"}
	export := BuildWorkflowExport([]*taskmodels.Workflow{wf}, map[string][]*WorkflowStep{"wf": {{ID: "step", Name: "Renamed gate", CompletionGatePolicy: CompletionGatePolicyQA}}}, nil)
	data, err := json.Marshal(export)
	require.NoError(t, err)
	var decoded map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &decoded))
	steps := decoded["workflows"].([]interface{})[0].(map[string]interface{})["steps"].([]interface{})
	require.Equal(t, "qa", steps[0].(map[string]interface{})["completion_gate_policy"])
}
