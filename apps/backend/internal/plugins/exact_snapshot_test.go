package plugins

import "testing"

func TestExactSnapshotCursorRejectsChangedAuthorityOrProjection(t *testing.T) {
	store := newExactSnapshotStore([]byte("test-secret"))
	binding := exactSnapshotBinding{InstallationID: "i1", WorkspaceID: "w1", FilterDigest: "f1", ApprovalRevision: 2, ProjectionVersion: "p1"}
	cursor, err := store.create(binding, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.offset(cursor, binding); err != nil {
		t.Fatal(err)
	}
	for _, binding := range []exactSnapshotBinding{{InstallationID: "i2", WorkspaceID: "w1", FilterDigest: "f1", ApprovalRevision: 2, ProjectionVersion: "p1"}, {InstallationID: "i1", WorkspaceID: "w2", FilterDigest: "f1", ApprovalRevision: 2, ProjectionVersion: "p1"}, {InstallationID: "i1", WorkspaceID: "w1", FilterDigest: "f1", ApprovalRevision: 3, ProjectionVersion: "p1"}, {InstallationID: "i1", WorkspaceID: "w1", FilterDigest: "f1", ApprovalRevision: 2, ProjectionVersion: "p2"}} {
		if _, err := store.offset(cursor, binding); err == nil {
			t.Fatalf("cursor accepted changed binding %#v", binding)
		}
	}
}
