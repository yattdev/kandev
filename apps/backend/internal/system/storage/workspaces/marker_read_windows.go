package workspaces

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func ownershipMarkerChangeStamp(file markerReadFile, _ os.FileInfo) (markerChangeStamp, error) {
	descriptor, ok := file.(interface{ Fd() uintptr })
	if !ok {
		return markerChangeStamp{}, errors.New("ownership marker native handle is unavailable")
	}
	var basic struct {
		CreationTime   int64
		LastAccessTime int64
		LastWriteTime  int64
		ChangeTime     int64
		FileAttributes uint32
		Padding        uint32
	}
	if err := windows.GetFileInformationByHandleEx(windows.Handle(descriptor.Fd()), windows.FileBasicInfo,
		(*byte)(unsafe.Pointer(&basic)), uint32(unsafe.Sizeof(basic))); err != nil {
		return markerChangeStamp{}, err
	}
	return markerChangeStamp{nanoseconds: basic.ChangeTime}, nil
}
