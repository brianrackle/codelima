package terminalstate

import "testing"

func TestCloneColorsOwnsEveryMutableField(t *testing.T) {
	foreground, background, cursor := uint32(1), uint32(2), uint32(3)
	original := &Colors{Foreground: &foreground, Background: &background, Cursor: &cursor, Palette: make([]uint32, 256), Theme: 1}
	copy := CloneColors(original)
	foreground, background, cursor, original.Palette[0] = 4, 5, 6, 7
	if *copy.Foreground != 1 || *copy.Background != 2 || *copy.Cursor != 3 || copy.Palette[0] != 0 {
		t.Fatalf("color policy retained a mutable borrow: %+v", copy)
	}
	if CloneColors(nil) != nil {
		t.Fatal("nil defaults changed meaning")
	}
}

func TestValidateColorsBounds(t *testing.T) {
	badRGB := uint32(0x1000000)
	for _, colors := range []*Colors{{Foreground: &badRGB}, {Palette: []uint32{1}}, {Theme: 3}} {
		if err := ValidateColors(colors); err == nil {
			t.Fatalf("invalid colors accepted: %+v", colors)
		}
	}
	palette := make([]uint32, 256)
	palette[255] = badRGB
	if err := ValidateColors(&Colors{Palette: palette}); err == nil {
		t.Fatal("oversized palette RGB accepted")
	}
}
