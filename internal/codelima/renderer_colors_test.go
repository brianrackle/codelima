//go:build darwin || linux

package codelima

import "testing"

func TestRendererColorIntentSurvivesUnavailableWorkerWithoutAliasingCaller(t *testing.T) {
	supervisor := &rendererSupervisor{terminalID: "colors"}
	foreground := uint32(0x123456)
	colors := &TerminalColors{Foreground: &foreground, Palette: make([]uint32, 256), Theme: 1}
	colors.Palette[0] = 0xabcdef
	if _, err := supervisor.Interact(TerminalInteractionRequest{Action: "colors", Colors: colors}); err == nil {
		t.Fatal("unavailable worker unexpectedly acknowledged policy")
	}
	foreground, colors.Palette[0] = 0, 0
	if supervisor.colors == nil || *supervisor.colors.Foreground != 0x123456 || supervisor.colors.Palette[0] != 0xabcdef {
		t.Fatalf("policy intent lost or caller owns retained memory: %+v", supervisor.colors)
	}
	journal := newRendererJournal(1024)
	journal.AppendResize(80, 24)
	bundle, err := decodeRendererRecovery("colors", supervisor.handoffRecovery(journal.Snapshot(), false))
	if err != nil || bundle.Colors == nil || *bundle.Colors.Foreground != 0x123456 {
		t.Fatalf("checkpoint-free handoff lost app policy: %+v err=%v", bundle, err)
	}
	revision := supervisor.colorsRevision
	invalid := uint32(0x1000000)
	if _, err := supervisor.Interact(TerminalInteractionRequest{Action: "colors", Colors: &TerminalColors{Foreground: &invalid}}); err == nil {
		t.Fatal("invalid color policy accepted")
	}
	if supervisor.colorsRevision != revision {
		t.Fatal("invalid policy replaced retained intent")
	}
}
