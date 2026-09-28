package plugins

import (
	"context"
	"testing"
	"time"
)

type recordingExactCommandBridge struct {
	grants, revokes []CapabilityApproval
	receipts        []ApprovalReceipt
	err             error
}

func (b *recordingExactCommandBridge) Grant(_ context.Context, a CapabilityApproval, _ string) error {
	b.grants = append(b.grants, a)
	return b.err
}
func (b *recordingExactCommandBridge) Revoke(_ context.Context, a CapabilityApproval, _ string) error {
	b.revokes = append(b.revokes, a)
	return b.err
}
func (b *recordingExactCommandBridge) RecordReceipt(_ context.Context, r ApprovalReceipt) error {
	b.receipts = append(b.receipts, r)
	return b.err
}

func TestExactCommandApprovalBridgeProjectsGrantReceiptAndRevoke(t *testing.T) {
	svc := &Service{}
	if err := svc.SetPluginsDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	bridge := &recordingExactCommandBridge{}
	svc.SetExactTaskCommandApprovalBridge(bridge)
	approval, err := svc.approvalGrant("inst", "workspace", 1, "digest", []string{"host.v2.write:tasks"}, "human", "grant", "grant-audit")
	if err != nil {
		t.Fatal(err)
	}
	if len(bridge.grants) != 1 || bridge.grants[0].Revision != approval.Revision {
		t.Fatalf("grants = %#v", bridge.grants)
	}
	receipt := ApprovalReceipt{InstallationID: "inst", WorkspaceID: "workspace", CapabilityID: "host.v2.write:tasks", Revision: 1, AuditID: "decision-audit", Result: approvalReceiptAllowed, ObservedAt: time.Now().UTC()}
	if err = svc.recordExactReadReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	if len(bridge.receipts) != 1 || bridge.receipts[0].AuditID != receipt.AuditID {
		t.Fatalf("receipts = %#v", bridge.receipts)
	}
	if _, err = svc.approvalRevoke("inst", "workspace", "human", "revoke", "revoke-audit"); err != nil {
		t.Fatal(err)
	}
	if len(bridge.revokes) != 1 || bridge.revokes[0].Revision != 1 {
		t.Fatalf("revokes = %#v", bridge.revokes)
	}
}
