package workspaces

import (
	"errors"
	"io"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func snapshotOpenFile(file io.ReadCloser) (ObjectSnapshot, error) {
	descriptor, ok := file.(interface{ Fd() uintptr })
	if !ok {
		return ObjectSnapshot{}, errors.New("native file identity is unavailable")
	}
	return snapshotWindowsHandle(windows.Handle(descriptor.Fd()))
}

func snapshotWindowsHandle(handle windows.Handle) (ObjectSnapshot, error) {
	kind, err := windows.GetFileType(handle)
	if err != nil || kind != windows.FILE_TYPE_DISK {
		return ObjectSnapshot{}, errors.New("native disk object identity is unavailable")
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return ObjectSnapshot{}, err
	}
	var basic struct {
		CreationTime, LastAccessTime, LastWriteTime, ChangeTime int64
		FileAttributes, Padding                                 uint32
	}
	if err := windows.GetFileInformationByHandleEx(handle, windows.FileBasicInfo,
		(*byte)(unsafe.Pointer(&basic)), uint32(unsafe.Sizeof(basic))); err != nil {
		return ObjectSnapshot{}, err
	}
	mode := os.FileMode(0o666)
	if info.FileAttributes&windows.FILE_ATTRIBUTE_READONLY != 0 {
		mode = 0o444
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		mode |= os.ModeSymlink
	} else if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		mode |= os.ModeDir | 0o111
	}
	return ObjectSnapshot{
		Mode: mode, Size: int64(uint64(info.FileSizeHigh)<<32 | uint64(info.FileSizeLow)),
		identity: [3]uint64{uint64(info.VolumeSerialNumber), uint64(info.FileIndexHigh), uint64(info.FileIndexLow)},
		modified: [2]int64{basic.LastWriteTime}, changed: [2]int64{basic.ChangeTime},
	}, nil
}

func (h *windowsDirectoryHandle) Snapshot() (ObjectSnapshot, error) {
	if h == nil || h.targetHandle == 0 {
		return ObjectSnapshot{}, errors.New("directory handle is closed")
	}
	return snapshotWindowsHandle(h.targetHandle)
}

func (h *windowsDirectoryHandle) SnapshotEntry(name string) (ObjectSnapshot, error) {
	handle, err := h.openSnapshotEntry(name)
	if err != nil {
		return ObjectSnapshot{}, err
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	return snapshotWindowsHandle(handle)
}

func (h *windowsDirectoryHandle) ReadLinkSnapshot(name string) (string, ObjectSnapshot, error) {
	handle, err := h.openSnapshotEntry(name)
	if err != nil {
		return "", ObjectSnapshot{}, err
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	before, err := snapshotWindowsHandle(handle)
	if err != nil {
		return "", ObjectSnapshot{}, err
	}
	target, err := readWindowsLinkHandle(handle)
	if err != nil {
		return "", ObjectSnapshot{}, err
	}
	after, err := snapshotWindowsHandle(handle)
	if err != nil {
		return "", ObjectSnapshot{}, err
	}
	current, err := h.SnapshotEntry(name)
	if err != nil || before != after || before != current {
		return "", ObjectSnapshot{}, errors.New("symbolic link changed while its target was read")
	}
	return target, before, nil
}

func (h *windowsDirectoryHandle) openSnapshotEntry(name string) (windows.Handle, error) {
	if h == nil || h.targetHandle == 0 {
		return 0, errors.New("directory handle is closed")
	}
	if err := validateDirectoryEntryName(name); err != nil {
		return 0, err
	}
	return openWindowsDependencyHandle(h.targetHandle, name, windowsDependencyReadAccess, 0)
}
