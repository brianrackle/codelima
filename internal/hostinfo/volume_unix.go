//go:build linux || darwin

package hostinfo

import (
	"errors"
	"math"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func readVolume(path string, snapshot *Snapshot) {
	if path == "" {
		return
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return
	}
	for {
		var stat unix.Statfs_t
		err := unix.Statfs(path, &stat)
		if err == nil {
			snapshot.DiskTotalBytes, snapshot.DiskAvailableBytes = volumeBytes(stat)
			snapshot.VolumePath = path
			return
		}
		if !errors.Is(err, os.ErrNotExist) {
			return
		}
		parent := filepath.Dir(path)
		if parent == path {
			return
		}
		path = parent
	}
}

func volumeBytes(stat unix.Statfs_t) (total, available *uint64) {
	size := volumeBlockSize(stat)
	if size == 0 || stat.Blocks == 0 || stat.Blocks > math.MaxUint64/size || stat.Bavail > stat.Blocks {
		return nil, nil
	}
	t, a := stat.Blocks*size, stat.Bavail*size
	return &t, &a
}
