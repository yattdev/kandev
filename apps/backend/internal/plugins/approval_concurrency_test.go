package plugins

import (
	"context"
	"errors"
	"testing"
	"time"
)

type pausingExactApprovalRevoke struct {
	ExactTaskCommandApprovalBridge
	entered chan struct{}
	release chan struct{}
}

func (b *pausingExactApprovalRevoke) Revoke(ctx context.Context, approval CapabilityApproval, auditID string) error {
	close(b.entered)
	<-b.release
	return b.ExactTaskCommandApprovalBridge.Revoke(ctx, approval, auditID)
}

func TestPublicRevokeSerializesWithReplacementGrant(t *testing.T) {
	ctx := context.Background()
	repo, _ := newExactCommandCompositionRepository(t)
	svc, _, _ := newTestService(t)
	bridge, err := NewSQLiteExactTaskCommandApprovalBridge(repo)
	if err != nil {
		t.Fatal(err)
	}
	svc.SetExactTaskCommandApprovalBridge(bridge)
	record, err := svc.Install(ctx, exactTaskAccessPackage(t, "kandev-plugin-exact-revoke-race"))
	if err != nil {
		t.Fatal(err)
	}
	digest := ManifestCapabilityDigest(record.Manifest)
	capabilities := []string{"host.v2.write:tasks"}
	if _, err = svc.GrantCapabilityApproval(record.InstallationID, "workspace-1", 1, digest, capabilities, "human", "grant", "grant-audit"); err != nil {
		t.Fatal(err)
	}
	paused := &pausingExactApprovalRevoke{ExactTaskCommandApprovalBridge: bridge, entered: make(chan struct{}), release: make(chan struct{})}
	svc.SetExactTaskCommandApprovalBridge(paused)
	revokeDone := make(chan error, 1)
	go func() {
		_, revokeErr := svc.RevokeCapabilityApproval(record.InstallationID, "workspace-1", 1, "human", "revoke", "revoke-audit")
		revokeDone <- revokeErr
	}()
	<-paused.entered
	grantDone := make(chan error, 1)
	go func() {
		_, grantErr := svc.GrantCapabilityApproval(record.InstallationID, "workspace-1", 2, digest, capabilities, "human", "replace", "replace-audit")
		grantDone <- grantErr
	}()
	var grantCompletedEarly bool
	select {
	case <-grantDone:
		grantCompletedEarly = true
	case <-time.After(100 * time.Millisecond):
	}
	close(paused.release)
	if err = <-revokeDone; err != nil {
		t.Fatalf("revoke revision 1: %v", err)
	}
	if !grantCompletedEarly {
		err = <-grantDone
	}
	if grantCompletedEarly || !errors.Is(err, ErrApprovalRevisionConflict) {
		t.Fatalf("replacement raced revoke: completed early=%v, grant error=%v", grantCompletedEarly, err)
	}
	current, ok, err := svc.GetCapabilityApproval(record.InstallationID, "workspace-1")
	if err != nil || !ok || current.Revision != 2 || current.State != string(ApprovalStateRevoked) {
		t.Fatalf("approval after revoke/replacement: current=%+v, ok=%v, err=%v", current, ok, err)
	}
}
