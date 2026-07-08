package codelima

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestNodeStartRollbackLogsStoreFailure covers absorbed work item 0.7.2: the
// NodeStart failure-path rollback writes are now checked and logged at error
// level instead of being discarded with `_ =`. It injects a store failure by
// making the node metadata directory read-only, then drives the rollback seam
// and asserts an error record is emitted (behaviour otherwise unchanged).
func TestNodeStartRollbackLogsStoreFailure(t *testing.T) {
	ctx := context.Background()
	service, workspace := newTestService(t)

	project, err := service.ProjectCreate(ctx, ProjectCreateInput{Slug: "root", WorkspacePath: workspace})
	if err != nil {
		t.Fatalf("ProjectCreate() error = %v", err)
	}
	node, err := service.NodeCreate(ctx, NodeCreateInput{Project: project.ID, Slug: "root-node"})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}
	bootstrap, err := service.store.LoadBootstrapState(node.ID)
	if err != nil {
		t.Fatalf("LoadBootstrapState() error = %v", err)
	}

	nodeDir := filepath.Join(service.cfg.MetadataRoot, "nodes", node.ID)
	if err := os.Chmod(nodeDir, 0o500); err != nil {
		t.Fatalf("Chmod(node dir read-only) error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(nodeDir, 0o755) })

	// Root (common in CI containers) bypasses directory permissions, so confirm
	// the read-only dir actually rejects a write before asserting on the log.
	if probe, probeErr := os.CreateTemp(nodeDir, "probe-"); probeErr == nil {
		_ = probe.Close()
		_ = os.Remove(probe.Name())
		t.Skip("filesystem permits writes to a 0500 dir (running as root?); cannot inject store failure")
	}

	var buf bytes.Buffer
	service.SetLogger(newTextLogger(&buf, slog.LevelError), slog.LevelError)

	node.Status = NodeStatusFailed
	node.UpdatedAt = service.now()
	service.recordNodeStartRollback(node, bootstrap, Event{
		Timestamp: service.now(),
		Type:      "node.start.failed",
		Fields:    map[string]any{"error": "forced start failure"},
	})

	logs := buf.String()
	if !strings.Contains(logs, "node start rollback save failed") {
		t.Fatalf("expected rollback save failure to be logged at error level, got %q", logs)
	}
	if !strings.Contains(logs, node.ID) {
		t.Fatalf("expected rollback log to name the node %q, got %q", node.ID, logs)
	}
	if !strings.Contains(logs, "level=ERROR") {
		t.Fatalf("expected rollback log to be at error level, got %q", logs)
	}
}

// TestNodeStartRollbackSilentWhenStoreHealthy proves the seam does not spam the
// log on the healthy path: a successful rollback write emits no error record.
func TestNodeStartRollbackSilentWhenStoreHealthy(t *testing.T) {
	ctx := context.Background()
	service, workspace := newTestService(t)

	project, err := service.ProjectCreate(ctx, ProjectCreateInput{Slug: "root", WorkspacePath: workspace})
	if err != nil {
		t.Fatalf("ProjectCreate() error = %v", err)
	}
	node, err := service.NodeCreate(ctx, NodeCreateInput{Project: project.ID, Slug: "root-node"})
	if err != nil {
		t.Fatalf("NodeCreate() error = %v", err)
	}
	bootstrap, err := service.store.LoadBootstrapState(node.ID)
	if err != nil {
		t.Fatalf("LoadBootstrapState() error = %v", err)
	}

	var buf bytes.Buffer
	service.SetLogger(newTextLogger(&buf, slog.LevelDebug), slog.LevelDebug)

	service.recordNodeStartRollback(node, bootstrap, Event{Timestamp: time.Now(), Type: "node.start.failed"})

	if strings.Contains(buf.String(), "rollback") {
		t.Fatalf("healthy rollback should not log an error, got %q", buf.String())
	}
}
