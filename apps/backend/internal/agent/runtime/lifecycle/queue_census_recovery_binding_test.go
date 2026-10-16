package lifecycle

import (
	"testing"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

func TestRecoveredQueueCensusOwnerComesFromDurableRecord(t *testing.T) {
	record := &models.ExecutorRunning{Metadata: map[string]interface{}{
		metadataKeyQueueCensusOwner: "owner-a", metadataKeyQueueCensusWorkspace: "workspace-a",
		metadataKeyQueueCensusIncarnation: "incarnation-a", metadataKeyQueueCensusExecution: "execution-a",
	}}
	instance := &ExecutorInstance{Metadata: map[string]interface{}{
		metadataKeyQueueCensusOwner: "owner-b", metadataKeyQueueCensusWorkspace: "workspace-b",
		metadataKeyQueueCensusIncarnation: "incarnation-b", metadataKeyQueueCensusExecution: "execution-b",
	}}
	metadata := recoveredRemoteMetadata(record, instance)
	require.Equal(t, "owner-a", metadata[metadataKeyQueueCensusOwner])
	require.Equal(t, "workspace-a", metadata[metadataKeyQueueCensusWorkspace])
	require.Equal(t, "incarnation-a", metadata[metadataKeyQueueCensusIncarnation])
	require.Equal(t, "execution-a", metadata[metadataKeyQueueCensusExecution])
	record.Metadata = nil
	metadata = recoveredRemoteMetadata(record, instance)
	require.NotContains(t, metadata, metadataKeyQueueCensusOwner)
}

func TestPluginCheckpointKeepsLaunchQueueOwner(t *testing.T) {
	metadata, err := mergePluginExecutorRuntimeMetadata(map[string]interface{}{
		MetadataKeyPluginExecutor: "typed", metadataKeyQueueCensusOwner: "owner-a",
		metadataKeyQueueCensusWorkspace: "workspace-a",
	}, map[string]interface{}{
		metadataKeyQueueCensusOwner: "owner-b", metadataKeyQueueCensusWorkspace: "workspace-b",
	})
	require.NoError(t, err)
	require.Equal(t, "owner-a", metadata[metadataKeyQueueCensusOwner])
	require.Equal(t, "workspace-a", metadata[metadataKeyQueueCensusWorkspace])
}
