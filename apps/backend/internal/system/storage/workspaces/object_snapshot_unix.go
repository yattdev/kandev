//go:build !windows

package workspaces

import (
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

func snapshotOpenFile(file io.ReadCloser) (ObjectSnapshot, error) {
	descriptor, ok := file.(interface{ Fd() uintptr })
	if !ok {
		return ObjectSnapshot{}, errors.New("native file identity is unavailable")
	}
	return snapshotUnixFD(int(descriptor.Fd()))
}

func snapshotUnixFD(fd int) (ObjectSnapshot, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return ObjectSnapshot{}, err
	}
	return snapshotUnixStat(stat)
}

func (h *unixDirectoryHandle) Snapshot() (ObjectSnapshot, error) {
	if h == nil || h.targetFD < 0 {
		return ObjectSnapshot{}, errors.New("directory handle is closed")
	}
	return snapshotUnixFD(h.targetFD)
}

func (h *unixDirectoryHandle) SnapshotEntry(name string) (ObjectSnapshot, error) {
	if h == nil || h.targetFD < 0 {
		return ObjectSnapshot{}, errors.New("directory handle is closed")
	}
	if err := validateDirectoryEntryName(name); err != nil {
		return ObjectSnapshot{}, err
	}
	var stat unix.Stat_t
	if err := unix.Fstatat(h.targetFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return ObjectSnapshot{}, err
	}
	return snapshotUnixStat(stat)
}

func (h *unixDirectoryHandle) ReadLinkSnapshot(name string) (string, ObjectSnapshot, error) {
	before, err := h.SnapshotEntry(name)
	if err != nil {
		return "", ObjectSnapshot{}, err
	}
	if before.Mode&os.ModeSymlink == 0 {
		return "", ObjectSnapshot{}, errors.New("directory entry is not a symbolic link")
	}
	if before.Size < 0 || before.Size > 64<<10 {
		return "", ObjectSnapshot{}, errors.New("symbolic link target exceeds inspection limit")
	}
	buffer := make([]byte, before.Size+1)
	n, err := unix.Readlinkat(h.targetFD, name, buffer)
	if err != nil {
		return "", ObjectSnapshot{}, err
	}
	if int64(n) != before.Size {
		return "", ObjectSnapshot{}, errors.New("symbolic link target changed during read")
	}
	after, err := h.SnapshotEntry(name)
	if err != nil {
		return "", ObjectSnapshot{}, err
	}
	if before != after {
		return "", ObjectSnapshot{}, errors.New("symbolic link changed while its target was read")
	}
	return string(buffer[:n]), before, nil
}

func unixSnapshotMode(raw uint64) os.FileMode {
	mode := os.FileMode(raw & 0o777)
	switch raw & unix.S_IFMT {
	case unix.S_IFDIR:
		mode |= os.ModeDir
	case unix.S_IFLNK:
		mode |= os.ModeSymlink
	case unix.S_IFIFO:
		mode |= os.ModeNamedPipe
	case unix.S_IFSOCK:
		mode |= os.ModeSocket
	case unix.S_IFCHR:
		mode |= os.ModeDevice | os.ModeCharDevice
	case unix.S_IFBLK:
		mode |= os.ModeDevice
	}
	if raw&0o4000 != 0 {
		mode |= os.ModeSetuid
	}
	if raw&0o2000 != 0 {
		mode |= os.ModeSetgid
	}
	if raw&0o1000 != 0 {
		mode |= os.ModeSticky
	}
	return mode
}
