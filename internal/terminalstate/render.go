package terminalstate

import (
	"go.rockorager.dev/vaxis"
	"strings"
)

func CellStyle(cell SnapshotCell) vaxis.Style {
	style := vaxis.Style{}
	if !cell.FGDefault {
		style.Foreground = vaxis.HexColor(cell.FG)
	}
	if !cell.BGDefault {
		style.Background = vaxis.HexColor(cell.BG)
	}
	if cell.Bold {
		style.Attribute |= vaxis.AttrBold
	}
	if cell.Faint {
		style.Attribute |= vaxis.AttrDim
	}
	if cell.Italic {
		style.Attribute |= vaxis.AttrItalic
	}
	if cell.Strikethrough {
		style.Attribute |= vaxis.AttrStrikethrough
	}
	if cell.Overline {
		style.Attribute |= vaxis.AttrOverline
	}
	if cell.Inverse {
		style.Attribute |= vaxis.AttrReverse
	}
	if cell.Invisible {
		style.Attribute |= vaxis.AttrInvisible
	}
	if cell.Blink {
		style.Attribute |= vaxis.AttrBlink
	}
	if cell.Selected {
		style.Attribute ^= vaxis.AttrReverse
	}
	if cell.Underline || cell.Hyperlink != "" {
		style.UnderlineStyle = vaxis.UnderlineSingle
	}
	switch cell.UnderlineStyle {
	case 2:
		style.UnderlineStyle = vaxis.UnderlineDouble
	case 3:
		style.UnderlineStyle = vaxis.UnderlineCurly
	case 4:
		style.UnderlineStyle = vaxis.UnderlineDotted
	case 5:
		style.UnderlineStyle = vaxis.UnderlineDashed
	}
	style.Hyperlink = cell.Hyperlink
	return style
}

func SnapshotText(snapshot Snapshot) string {
	lines := make([]string, 0, snapshot.Rows)
	for row := 0; row < snapshot.Rows; row++ {
		var line strings.Builder
		for col := 0; col < snapshot.Cols; col++ {
			index := row*snapshot.Cols + col
			if index >= len(snapshot.Cells) {
				break
			}
			cell := snapshot.Cells[index]
			if cell.Width == 0 {
				continue
			}
			grapheme := cell.Grapheme
			if grapheme == "" {
				grapheme = " "
			}
			line.WriteString(grapheme)
		}
		lines = append(lines, strings.TrimRight(line.String(), " "))
	}
	return strings.Join(lines, "\n")
}
