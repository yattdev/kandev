//go:build !linux && !darwin && !windows

package workspaces

import (
	"errors"

	"golang.org/x/sys/unix"
)

func snapshotUnixStat(_ unix.Stat_t) (ObjectSnapshot, error) {
	return ObjectSnapshot{}, errors.New("native object change identity is unsupported")
}
