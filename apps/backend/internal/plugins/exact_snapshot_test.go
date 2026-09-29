package plugins

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestConnectionExactSnapshotStoresUseDistinctRandomSecrets(t *testing.T) {
	first, err := newConnectionExactSnapshotStore()
	if err != nil {
		t.Fatal(err)
	}
	second, err := newConnectionExactSnapshotStore()
	if err != nil {
		t.Fatal(err)
	}
	if len(first.secret) != exactSnapshotSecretBytes {
		t.Fatalf("secret length = %d", len(first.secret))
	}
	if string(first.secret) == string(second.secret) {
		t.Fatal("connection snapshot secrets matched")
	}
}

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

func TestExactSnapshotCursorExpires(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newExactSnapshotStore([]byte("test-secret"))
		binding := exactSnapshotBinding{InstallationID: "i1", WorkspaceID: "w1", FilterDigest: "f1", ApprovalRevision: 2, ProjectionVersion: "p1"}
		cursor, err := store.create(binding, 1)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Minute)
		if _, err := store.offset(cursor, binding); err == nil {
			t.Fatal("expired exact snapshot cursor was accepted")
		}
	})
}
