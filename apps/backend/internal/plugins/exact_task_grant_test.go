package plugins

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExactTaskGrantBindsScopeExpiresAndSurvivesRestart(t *testing.T) {
	ledger := newApprovalLedger(t.TempDir())
	now := time.Date(2026, 9, 28, 4, 0, 0, 0, time.UTC)
	req := ExactTaskGrantRequest{InstallationID: "installation-1", WorkspaceID: "workspace-1", TaskID: "task-1", CapabilityID: "host.v2.write:tasks", ApprovalAuditID: "receipt-1", ApprovalRevision: 2, ActionDigest: "action-1", IdempotencyKey: "key-1"}
	grant, err := ledger.issueExactTaskGrant(req, now)
	require.NoError(t, err)
	_, err = newApprovalLedger(ledger.dir).requireExactTaskGrant(grant.ID, req, now.Add(time.Minute))
	require.NoError(t, err)
	wrong := req
	wrong.TaskID = "task-2"
	_, err = ledger.requireExactTaskGrant(grant.ID, wrong, now.Add(time.Minute))
	require.ErrorIs(t, err, ErrExactTaskGrantUnavailable)
	_, err = ledger.requireExactTaskGrant(grant.ID, req, now.Add(exactTaskGrantTTL))
	require.True(t, errors.Is(err, ErrExactTaskGrantUnavailable))
}
