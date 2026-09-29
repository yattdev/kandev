package messagequeue

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRoutineWakeEnvelopeCanonicalKeyIgnoresCarrierIdentity(t *testing.T) {
	first := RoutineWakeEnvelope{
		WorkspaceID: "workspace", RoutineType: "wake", RoutineName: "cycle",
		PolicyGeneration: "policy-7", ScopeGeneration: "board-9", SourceID: "source-a",
	}
	second := first
	second.SourceID = "source-b"

	key, err := first.CanonicalKey()
	require.NoError(t, err)
	otherKey, err := second.CanonicalKey()
	require.NoError(t, err)
	require.Equal(t, key, otherKey)
}

func TestRoutineWakeEnvelopeCanonicalKeySeparatesGenerations(t *testing.T) {
	envelope := RoutineWakeEnvelope{
		WorkspaceID: "workspace", RoutineType: "wake", RoutineName: "cycle",
		PolicyGeneration: "policy-7", ScopeGeneration: "board-9", SourceID: "source-a",
	}
	key, err := envelope.CanonicalKey()
	require.NoError(t, err)

	envelope.ScopeGeneration = "board-10"
	otherKey, err := envelope.CanonicalKey()
	require.NoError(t, err)
	require.NotEqual(t, key, otherKey)
}

func TestRoutineWakeEnvelopeReceiptIsBodyFree(t *testing.T) {
	envelope := RoutineWakeEnvelope{
		WorkspaceID: "workspace", RoutineType: "wake", RoutineName: "cycle",
		PolicyGeneration: "policy-7", ScopeGeneration: "board-9", SourceID: "source-a",
	}
	receipt, err := envelope.Receipt()
	require.NoError(t, err)
	require.Equal(t, "source-a", receipt.SourceID)
	require.Equal(t, "wake", receipt.RoutineType)
	require.NotEmpty(t, receipt.CanonicalKey)
}

func TestRoutineWakeEnvelopeRejectsIncompleteIdentity(t *testing.T) {
	_, err := (RoutineWakeEnvelope{WorkspaceID: "workspace", RoutineType: "wake"}).CanonicalKey()
	require.Error(t, err)
}
