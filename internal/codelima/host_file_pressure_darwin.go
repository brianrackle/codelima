//go:build darwin

package codelima

import "golang.org/x/sys/unix"

func platformHostFilePressureSampler() (hostFilePressureSampler, bool) {
	return func() (hostFilePressure, error) {
		used, err := unix.SysctlUint32("kern.num_files")
		if err != nil {
			return hostFilePressure{}, err
		}
		limit, err := unix.SysctlUint32("kern.maxfiles")
		if err != nil {
			return hostFilePressure{}, err
		}
		return hostFilePressure{used: uint64(used), limit: uint64(limit)}, nil
	}, true
}
