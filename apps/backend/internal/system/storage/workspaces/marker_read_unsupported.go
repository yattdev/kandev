//go:build !linux && !darwin && !windows

package workspaces

import (
	"errors"
	"os"
)

func ownershipMarkerChangeStamp(_ markerReadFile, _ os.FileInfo) (markerChangeStamp, error) {
	return markerChangeStamp{}, errors.New("ownership marker change identity is unsupported")
}
