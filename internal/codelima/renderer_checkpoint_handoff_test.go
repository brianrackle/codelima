package codelima

import (
	"encoding/json"
	"testing"
)

func TestRendererRecoveryBundleRoundTripAndIntegrity(t *testing.T) {
	journal := newRendererJournal(1024)
	journal.AppendResize(80, 24)
	journal.AppendOutput([]byte("before"))
	checkpoint, err := newRendererCheckpoint(rendererNativeBuildIdentity(), "term", 1, 2, false, terminalCheckpointState{Data: []byte("native"), Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	journal.AppendResize(100, 30)
	journal.AppendOutput([]byte("after"))
	encoded, err := encodeRendererRecovery("term", checkpoint, journal.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := decodeRendererRecovery("term", encoded)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Checkpoint == nil || bundle.Journal.LastID != 4 || len(bundle.Journal.Events) != 4 {
		t.Fatalf("lost recovery state: %+v", bundle)
	}
	if _, err := decodeRendererRecovery("other", encoded); err == nil {
		t.Fatal("accepted another terminal's recovery")
	}
	bundle.Journal.Events[3].Data = []byte("modified")
	corrupt, _ := json.Marshal(bundle)
	if _, err := decodeRendererRecovery("term", corrupt); err == nil {
		t.Fatal("accepted modified ordered tail")
	}
}

func TestRendererRecoveryBundleCrossBuildKeepsOrderedRawFallback(t *testing.T) {
	journal := newRendererJournal(1024)
	journal.AppendResize(80, 24)
	checkpoint, err := newRendererCheckpoint("old-native-build", "term", 1, 1, false, terminalCheckpointState{Data: []byte("native"), Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	journal.AppendResize(100, 30)
	journal.AppendOutput([]byte("raw"))
	encoded, err := encodeRendererRecovery("term", checkpoint, journal.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := decodeRendererRecovery("term", encoded)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Checkpoint != nil || len(bundle.Journal.Events) != 2 || bundle.Journal.Events[0].Cols != 100 {
		t.Fatalf("incorrect cross-build fallback: %+v", bundle)
	}
	restored := journalFromRendererRecovery(bundle.Journal)
	if next := restored.AppendOutput([]byte("live")); next.ID != 4 {
		t.Fatalf("journal watermark reset: %d", next.ID)
	}
}

func TestRendererRecoveryBundleRawFallbackKeepsIndependentColorPolicy(t *testing.T) {
	journal := newRendererJournal(1024)
	journal.AppendResize(80, 24)
	foreground := uint32(0x123456)
	colors := &TerminalColors{Foreground: &foreground, Theme: 1}
	for _, build := range []string{"", "old-native-build"} {
		t.Run(build, func(t *testing.T) {
			var checkpoint *rendererCheckpoint
			if build != "" {
				var err error
				checkpoint, err = newRendererCheckpoint(build, "term", 1, 1, false, terminalCheckpointState{Data: []byte("native"), Cols: 80, Rows: 24})
				if err != nil {
					t.Fatal(err)
				}
			}
			encoded, err := encodeRendererRecovery("term", checkpoint, journal.Snapshot(), colors)
			if err != nil {
				t.Fatal(err)
			}
			bundle, err := decodeRendererRecovery("term", encoded)
			if err != nil {
				t.Fatal(err)
			}
			if bundle.Checkpoint != nil || bundle.Colors == nil || bundle.Colors.Foreground == nil || *bundle.Colors.Foreground != foreground || bundle.Colors.Theme != 1 {
				t.Fatalf("raw fallback lost app policy: %+v", bundle)
			}
		})
	}
}
