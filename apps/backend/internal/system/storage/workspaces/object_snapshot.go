package workspaces

import (
	"io"
	"os"
)

// ObjectSnapshot is an opaque, process-local observation of native identity and
// change metadata. It is not an ownership or quiescence proof.
type ObjectSnapshot struct {
	Mode     os.FileMode
	Size     int64
	identity [3]uint64
	modified [2]int64
	changed  [2]int64
}

// IdentityDirectoryHandle supplies bounded no-follow metadata for read-only
// inspection. Unavailable native metadata must fail closed.
type IdentityDirectoryHandle interface {
	DirectoryHandle
	Snapshot() (ObjectSnapshot, error)
	SnapshotEntry(name string) (ObjectSnapshot, error)
	ReadLinkSnapshot(name string) (string, ObjectSnapshot, error)
}

// SnapshotFile observes the object behind a native open descriptor.
func SnapshotFile(file io.ReadCloser) (ObjectSnapshot, error) {
	return snapshotOpenFile(file)
}
