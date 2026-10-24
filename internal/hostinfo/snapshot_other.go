//go:build !linux && !darwin

package hostinfo

func platformCapacity() (string, *uint64) { return "", nil }
func readVolume(_ string, _ *Snapshot)    {}
