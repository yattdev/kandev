package workspaces

import "golang.org/x/sys/unix"

func snapshotUnixStat(stat unix.Stat_t) (ObjectSnapshot, error) {
	return ObjectSnapshot{
		Mode: unixSnapshotMode(uint64(stat.Mode)), Size: stat.Size,
		identity: [3]uint64{stat.Dev, stat.Ino},
		modified: [2]int64{stat.Mtim.Sec, stat.Mtim.Nsec},
		changed:  [2]int64{stat.Ctim.Sec, stat.Ctim.Nsec},
	}, nil
}
