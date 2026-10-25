package codelima

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"golang.org/x/sys/unix"
)

func TestNodeMetricsGuestPlatformParsesIndependentUnknowns(t *testing.T) {
	for _, tc := range []struct {
		input, architecture string
		kvm                 *bool
	}{
		{"codelima-arch aarch64\ncodelima-kvm 1", "arm64", boolPointer(true)},
		{"codelima-arch x86_64\ncodelima-kvm 0", "amd64", boolPointer(false)},
		{"codelima-arch riscv64", "riscv64", nil},
		{"codelima-kvm 0", "", boolPointer(false)},
		{"codelima-arch \x1b[31maarch64\ncodelima-kvm yes", "", nil},
		{"codelima-arch " + strings.Repeat("a", 33) + "\ncodelima-kvm 1 extra", "", nil},
		{"codelima-arch aarch64\ncodelima-arch invalid value\ncodelima-kvm 1\ncodelima-kvm invalid", "", nil},
	} {
		d := parseForwardingGuestObservation([]byte(tc.input)).Details
		if d.Architecture != tc.architecture || (d.KVMAccessible == nil) != (tc.kvm == nil) || d.KVMAccessible != nil && *d.KVMAccessible != *tc.kvm {
			t.Errorf("platform from %q: %+v", tc.input, d)
		}
	}
}

func TestNodeMetricsGuestPlatformCommand(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("guest telemetry reads Linux procfs")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "sh", "-c", guestTelemetryCommand).Output()
	if err != nil {
		t.Fatal(err)
	}
	d := parseForwardingGuestObservation(output).Details
	if d.Architecture != runtime.GOARCH || d.KVMAccessible == nil {
		t.Fatalf("guest command omitted platform: %+v", d)
	}
	device, statErr := os.Stat("/dev/kvm")
	want := statErr == nil && device.Mode()&os.ModeCharDevice != 0 && unix.Access("/dev/kvm", unix.R_OK|unix.W_OK) == nil
	if *d.KVMAccessible != want {
		t.Fatalf("KVM access = %v, want %v for sampler identity", *d.KVMAccessible, want)
	}
	output, err = exec.CommandContext(ctx, "sh", "-c", "uname() { return 1; }\n"+guestTelemetryCommand).Output()
	if err != nil {
		t.Fatal(err)
	}
	d = parseForwardingGuestObservation(output).Details
	if d.Architecture != "" || d.KVMAccessible == nil || *d.KVMAccessible != want {
		t.Fatalf("failed architecture probe concealed independent KVM access: %+v", d)
	}
}

func TestForwarderMetricsPlatformSurvivesPushPollAndClears(t *testing.T) {
	now := time.Now()
	f := &dynamicForwarder{now: func() time.Time { return now }}
	f.recordNodeUsage("node", parseForwardingGuestObservation([]byte("codelima-arch aarch64\ncodelima-kvm 1")), now)
	usage := f.metricsSnapshot("node", "", time.Time{}).Latest
	if usage == nil || usage.Details == nil || usage.Details.Architecture != "arm64" || usage.Details.KVMAccessible == nil || !*usage.Details.KVMAccessible {
		t.Fatalf("platform-only sample lost: %+v", usage)
	}
	nodes := []Node{{ID: "node", LastRuntimeObservation: &RuntimeObservation{Status: ObservationRunning}}}
	f.addNodeUsage(nodes)
	observation := nodes[0].LastRuntimeObservation
	if observation.UsageDetails == nil || observation.UsageDetails.Architecture != "arm64" || !*observation.UsageDetails.KVMAccessible {
		t.Fatalf("node-list lost platform: %+v", observation)
	}
	applyNodeUsageSample(*usage, observation)
	*usage.Details.KVMAccessible = false
	if !*observation.UsageDetails.KVMAccessible || !*f.metricsSnapshot("node", "", time.Time{}).Latest.Details.KVMAccessible {
		t.Fatal("mutable platform sample crossed owner boundary")
	}
	f.clearNodeUsage("node")
	applyNodeUsageSample(*f.metricsSnapshot("node", "", time.Time{}).Latest, observation)
	if observation.UsageDetails != nil {
		t.Fatal("cleared telemetry retained platform")
	}
}

func TestTUIOverviewGuestPlatformUsesObservedFreshness(t *testing.T) {
	for _, size := range [][2]int{{150, 38}, {80, 24}} {
		a := overviewFixture(t, size[0], size[1])
		now := time.Now()
		node := &a.state.nodes[0]
		node.LastRuntimeObservation = &RuntimeObservation{Status: ObservationRunning, ResourceUsageSampledAt: &now, UsageDetails: &daemon.NodeUsageDetails{Architecture: "arm64", KVMAccessible: boolPointer(true)}}
		a.refreshFleet()
		a.draw()
		if screen := renderedScreenText(t, a.vx, size[0], size[1]); !strings.Contains(screen, "arm64 · KVM yes") {
			t.Fatalf("missing platform at %v:\n%s", size, screen)
		}
		old := now.Add(-time.Minute)
		node.LastRuntimeObservation.ResourceUsageSampledAt = &old
		if got := a.fleetGuestPlatformLabel(*node); got != "last: arm64 · KVM yes" {
			t.Fatalf("stale platform looked current: %q", got)
		}
		node.LastRuntimeObservation.ResourceUsageSampledAt = &now
		a.daemonDisconnected = true
		if !strings.HasPrefix(a.fleetGuestPlatformLabel(*node), "last:") {
			t.Fatal("disconnection did not mark platform stale")
		}
		node.LastRuntimeObservation.UsageDetails = nil
		if got := a.fleetGuestPlatformLabel(*node); got != "arch ? · KVM ?" {
			t.Fatalf("invented missing platform: %q", got)
		}
	}
}
