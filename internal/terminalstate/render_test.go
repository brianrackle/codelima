package terminalstate

import (
	"testing"

	"go.rockorager.dev/vaxis"
)

func TestCellStylePreservesOverlineWithSelectionAndOtherAttributes(t *testing.T) {
	style := CellStyle(SnapshotCell{FGDefault: true, BGDefault: true, Bold: true, Overline: true, Selected: true})
	want := vaxis.AttrBold | vaxis.AttrOverline | vaxis.AttrReverse
	if style.Attribute != want {
		t.Fatalf("attributes=%v, want %v", style.Attribute, want)
	}
	if style.Foreground != 0 || style.Background != 0 {
		t.Fatal("default colors became explicit colors")
	}
	style = CellStyle(SnapshotCell{FGDefault: true, BGDefault: true, Overline: true, Selected: true, Inverse: true})
	if style.Attribute != vaxis.AttrOverline {
		t.Fatalf("selection did not toggle inverse while preserving overline: %v", style.Attribute)
	}
}
