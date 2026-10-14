package mcp

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestStepCompleteForwardsCallingTurn(t *testing.T) {
	backend := &testBackend{}
	s := newTaskModeServer(t, backend, "task")
	result := callTool(t, s, "step_complete_kandev", map[string]interface{}{"summary": "complete", "turn_id": "immutable-turn", "verdict": "PASS"})
	require.False(t, result.IsError)
	require.Equal(t, "immutable-turn", backend.lastPayload.(map[string]interface{})["turn_id"])
}
