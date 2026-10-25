package codelima

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

func TestFleetEvidencePreviewReadsOnlySelectedRetainedMinute(t *testing.T) {
	s, node := setupBootstrapStepsTest(t)
	h := newDaemonHost(s)
	t.Cleanup(func() { _ = h.Close() })
	now := s.now()
	history := &nodeMetricsHistory{}
	used, total, cpu := uint64(3<<30), uint64(8<<30), 42.0
	for i := range 3600 {
		point := daemon.NodeMetricPoint{SampledAt: now.Add(time.Duration(i-3599) * time.Second), CPUUsagePercent: &cpu, MemoryUsedBytes: &used, MemoryTotalBytes: &total}
		if i == 3598 {
			point.MemoryUsedBytes = nil
		}
		history.appendPoint(point)
	}
	h.forwarder = &dynamicForwarder{metricsEpoch: "epoch", metricsHistory: map[string]*nodeMetricsHistory{node.ID: history}, now: func() time.Time { return now }}
	page := requestFleetEvidence(t, h, fleetEvidenceRequest{Nodes: []string{node.ID}, RecentNode: node.ID})
	p := page.Preview
	if p == nil || p.NodeID != node.ID || len(p.History) != 60 || p.MetricsEpoch != "epoch" || p.History[58].MemoryUsedBytes != nil || *p.History[59].MemoryUsedBytes != used {
		t.Fatalf("bad preview history: %+v", p)
	}
	*p.History[59].MemoryUsedBytes = 1
	page = requestFleetEvidence(t, h, fleetEvidenceRequest{Nodes: []string{node.ID}, RecentNode: node.ID})
	if *page.Preview.History[59].MemoryUsedBytes != used {
		t.Fatal("history alias escaped")
	}
	page = requestFleetEvidence(t, h, fleetEvidenceRequest{Nodes: []string{node.ID}})
	if page.Preview != nil {
		t.Fatal("hidden preview read history")
	}
}

func TestFleetPreviewRuntimeUsesEffectiveInstanceConfiguration(t *testing.T) {
	root := t.TempDir()
	c := &LimaClient{LimaHome: root}
	node := Node{SandboxName: "instance", DirectoryPath: "/work", GuestWorkspacePath: "/guest", WorkspaceMode: "mounted"}
	dir := filepath.Join(root, node.SandboxName)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "lima.yaml")
	data := "vmType: qemu\nmountType: 9p\nnestedVirtualization: false\nmounts:\n- location: /unrelated\n  writable: true\n- location: /work\n  mountPoint: /guest\n  writable: false\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := c.InspectConfiguration(node)
	if err != nil || config.VMType != "qemu" || config.MountType != "9p" || config.Nested == nil || *config.Nested || config.WorkspaceWritable == nil || *config.WorkspaceWritable || config.WorkspacePath != "/guest" {
		t.Fatalf("wrong effective configuration: %+v %v", config, err)
	}
	node.DirectoryPath = "/unknown"
	config, err = c.InspectConfiguration(node)
	if err != nil || config.WorkspaceWritable != nil {
		t.Fatalf("guessed an unrelated mount: %+v %v", config, err)
	}
	node.SandboxName = "../outside"
	if _, err := c.InspectConfiguration(node); err == nil {
		t.Fatal("accepted an escaping instance name")
	}
}
