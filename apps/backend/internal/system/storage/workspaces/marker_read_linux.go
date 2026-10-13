package workspaces

import (
	"errors"
	"os"
	"syscall"
)

func ownershipMarkerChangeStamp(_ markerReadFile, info os.FileInfo) (markerChangeStamp, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return markerChangeStamp{}, errors.New("ownership marker change identity is unavailable")
	}
	return markerChangeStamp{seconds: stat.Ctim.Sec, nanoseconds: stat.Ctim.Nsec}, nil
}
