package hostinfo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadVolumeUsesExistingAncestorWithoutCreatingHome(t *testing.T) {
	parent := t.TempDir()
	home := filepath.Join(parent, "new", "lima")
	snapshot := Read(home)
	if snapshot.CPUs < 1 || snapshot.ObservedAt.IsZero() {
		t.Fatalf("missing host identity: %+v", snapshot)
	}
	if snapshot.DiskAvailableBytes == nil || snapshot.DiskTotalBytes == nil || *snapshot.DiskAvailableBytes > *snapshot.DiskTotalBytes {
		t.Fatalf("invalid volume sample: %+v", snapshot)
	}
	if snapshot.VolumePath != parent {
		t.Fatalf("sampled %q, want existing ancestor %q", snapshot.VolumePath, parent)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("observation created the requested home: %v", err)
	}
}

func TestReadMissingVolumeIsUnknown(t *testing.T) {
	snapshot := Read("")
	if snapshot.DiskAvailableBytes != nil || snapshot.DiskTotalBytes != nil {
		t.Fatal("an unresolved Lima home must not report the working directory's free space")
	}
}
