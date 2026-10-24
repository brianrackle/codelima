// Package hostinfo provides read-only capacity observations of the machine
// running CodeLima. A missing measurement stays nil rather than becoming zero.
package hostinfo

import (
	"runtime"
	"time"
)

// Snapshot describes capacity, not current consumption. VM allocations are
// joined separately from the runtime's observed instance state.
type Snapshot struct {
	Model              string
	CPUs               int
	MemoryBytes        *uint64
	DiskAvailableBytes *uint64
	DiskTotalBytes     *uint64
	VolumePath         string
	ObservedAt         time.Time
}

// Read samples local capacity without creating directories or running commands.
// A missing Lima home is measured at its nearest existing ancestor. An empty
// or inaccessible home leaves volume capacity unknown.
func Read(limaHome string) Snapshot {
	snapshot := Snapshot{CPUs: runtime.NumCPU(), ObservedAt: time.Now().UTC()}
	snapshot.Model, snapshot.MemoryBytes = platformCapacity()
	readVolume(limaHome, &snapshot)
	return snapshot
}
