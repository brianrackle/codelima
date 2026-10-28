package codelima

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestNodeVMStatusSeparatesSetupFromRuntime(t *testing.T) {
	for _, state := range []NodeStatus{NodeStatusCreated, NodeStatusProvisioning, NodeStatusRegistering, NodeStatusFailed, NodeStatusTerminating, NodeStatusTerminated} {
		for _, runtime := range []ObservationStatus{ObservationRunning, ObservationStopped, ObservationInstalling, ObservationBroken} {
			node := Node{Status: state, LastRuntimeObservation: &RuntimeObservation{Exists: true, Status: runtime}}
			if got := nodeVMStatus(node); got != string(runtime) {
				t.Errorf("lifecycle %s, runtime %s: status = %q", state, runtime, got)
			}
		}
		node := Node{Status: state}
		if got := nodeVMStatus(node); got != "unknown" {
			t.Errorf("unobserved %s: status = %q", state, got)
		}
		node.LastRuntimeObservation = &RuntimeObservation{Exists: false}
		if got := nodeVMStatus(node); got != "missing" {
			t.Errorf("missing %s: status = %q", state, got)
		}
	}
}

func TestNodeSetupStatus(t *testing.T) {
	for _, test := range []struct {
		name string
		node Node
		want string
	}{
		{"created", Node{Status: NodeStatusCreated}, "pending"},
		{"incomplete running VM", Node{Status: NodeStatusRunning}, "pending"},
		{"installing", Node{Status: NodeStatusProvisioning}, "provisioning"},
		{"registering", Node{Status: NodeStatusRegistering}, "registering"},
		{"failed", Node{Status: NodeStatusFailed}, "failed"},
		{"saved failure", Node{Status: NodeStatusRunning, LifecycleState: NodeStatusFailed}, "failed"},
		{"ready running", Node{Status: NodeStatusRunning, BootstrapCompleted: true}, "ready"},
		{"ready stopped", Node{Status: NodeStatusStopped, BootstrapCompleted: true}, "ready"},
		{"validation failed after setup", Node{Status: NodeStatusFailed, BootstrapCompleted: true}, "failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := nodeSetupStatus(test.node); got != test.want {
				t.Fatalf("setup = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCLINodeOutputSeparatesVMStatusAndSetup(t *testing.T) {
	node := Node{Slug: "running-failed-setup", Status: NodeStatusFailed, LastRuntimeObservation: &RuntimeObservation{Exists: true, Status: ObservationRunning}}
	for _, asJSON := range []bool{false, true} {
		var output bytes.Buffer
		writeSuccess(&output, asJSON, node)
		var data map[string]any
		if asJSON {
			var envelope struct{ Data map[string]any }
			if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			data = envelope.Data
		} else if err := yaml.Unmarshal(output.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		if data["status"] != "running" || data["setup_status"] != "failed" {
			t.Fatalf("JSON=%v lost independent states: %s", asJSON, output.String())
		}
	}
	var output bytes.Buffer
	writeSuccess(&output, true, []Node{node})
	var envelope struct{ Data []map[string]any }
	if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data) != 1 || envelope.Data[0]["status"] != "running" || envelope.Data[0]["setup_status"] != "failed" {
		t.Fatalf("list JSON lost independent states: %s", output.String())
	}
	table := renderNodeList([]Node{node})
	if !strings.Contains(table, "setup") || !strings.Contains(table, "running") || !strings.Contains(table, "failed") {
		t.Fatalf("list missing VM/setup columns: %s", table)
	}
	if node.Status != NodeStatusFailed {
		t.Fatal("presentation erased the failure needed by retry")
	}
}
