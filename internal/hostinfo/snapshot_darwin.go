package hostinfo

import "golang.org/x/sys/unix"

func volumeBlockSize(stat unix.Statfs_t) uint64 {
	if stat.Bsize > 0 {
		return uint64(stat.Bsize)
	}
	return 0
}

func platformCapacity() (string, *uint64) {
	model, _ := unix.Sysctl("machdep.cpu.brand_string")
	if model == "" {
		model, _ = unix.Sysctl("hw.model")
	}
	memory, err := unix.SysctlUint64("hw.memsize")
	if err != nil || memory == 0 {
		return model, nil
	}
	return model, &memory
}
