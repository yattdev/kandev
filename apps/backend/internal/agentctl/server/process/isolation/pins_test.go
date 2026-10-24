//go:build linux

package isolation

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPinRejectsReplacedDirectory(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared")
	if err := os.Mkdir(shared, 0700); err != nil {
		t.Fatal(err)
	}
	identity, err := Capture(shared)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(shared, shared+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(shared, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Pin(identity); err == nil {
		t.Fatal("pin accepted a replacement directory")
	}
}

func TestPinRejectsSymlinkReplacement(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared")
	if err := os.Mkdir(shared, 0700); err != nil {
		t.Fatal(err)
	}
	identity, err := Capture(shared)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(shared, shared+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shared+"-old", shared); err != nil {
		t.Fatal(err)
	}
	if _, err := Pin(identity); err == nil {
		t.Fatal("pin followed a replacement symlink")
	}
}

func TestPinnedDirectoryDetectsReplacementAndClosesDescriptor(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared")
	if err := os.Mkdir(shared, 0700); err != nil {
		t.Fatal(err)
	}
	identity, err := Capture(shared)
	if err != nil {
		t.Fatal(err)
	}
	pin, err := Pin(identity)
	if err != nil {
		t.Fatal(err)
	}
	fd := int(pin.File().Fd())
	if err := os.Rename(shared, shared+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(shared, 0700); err != nil {
		t.Fatal(err)
	}
	if err := pin.Validate(); err == nil {
		t.Fatal("pinned object accepted a changed path")
	}
	if err := pin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := unix.Fstat(fd, &unix.Stat_t{}); err != unix.EBADF {
		t.Fatalf("closed descriptor Fstat error = %v, want EBADF", err)
	}
}

func TestPinnedObjectRejectsClosedDescriptor(t *testing.T) {
	root := t.TempDir()
	identity, err := Capture(root)
	if err != nil {
		t.Fatal(err)
	}
	pin, err := Pin(identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := pin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := pin.Validate(); err == nil {
		t.Fatal("closed descriptor validated as a retained isolation object")
	}
}

func TestPinRetainsMissingLeafParent(t *testing.T) {
	root := t.TempDir()
	leaf := filepath.Join(root, "refs", "heads", "ref.lock")
	identity, err := Capture(leaf)
	if err != nil {
		t.Fatal(err)
	}
	pin, err := Pin(identity)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pin.Close() }()
	if pin.File() == nil || pin.Leaf() != filepath.Join("refs", "heads", "ref.lock") || identity.Exists {
		t.Fatalf("missing leaf pin = %+v, identity = %+v", pin, identity)
	}
}

func TestPinRejectsMaterializedMissingLeaf(t *testing.T) {
	root := t.TempDir()
	leaf := filepath.Join(root, "ref.lock")
	identity, err := Capture(leaf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leaf, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Pin(identity); err == nil {
		t.Fatal("pin accepted a materialized lock file")
	}
}

func TestPinnedSetRetainsAndClosesGrantDescriptors(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared")
	if err := os.Mkdir(shared, 0700); err != nil {
		t.Fatal(err)
	}
	identity, err := Capture(shared)
	if err != nil {
		t.Fatal(err)
	}
	set, err := PinAll([]Identity{identity})
	if err != nil {
		t.Fatal(err)
	}
	pin, ok := set.Get(shared)
	if !ok || pin.File() == nil {
		t.Fatal("pin set lost grant descriptor")
	}
	fd := int(pin.File().Fd())
	if err := set.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := set.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := set.Get(shared); ok {
		t.Fatal("closed pin set retained accessible grant")
	}
	if err := unix.Fstat(fd, &unix.Stat_t{}); err != unix.EBADF {
		t.Fatalf("closed descriptor Fstat error = %v, want EBADF", err)
	}
}
