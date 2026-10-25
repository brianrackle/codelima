package codelima

import (
	"fmt"
	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/hostinfo"
	"math"
	"testing"
	"time"

	"go.rockorager.dev/vaxis"
)

func TestTUIGaugeTrackRendering(t *testing.T) {
	for _, rgb := range []bool{false, true} {
		theme := newTUITheme(rgb)
		for _, value := range []float64{0, 25, 70, 90, 150, math.NaN(), -1, math.Inf(1)} {
			vx := newRenderTestVaxis(t, 20, 2)
			base := theme.selected
			base.Attribute |= vaxis.AttrBold
			line := tuiGaugeLine{prefix: "░ ", percent: value, cells: 10, suffix: " end"}
			line.draw(vx.Window(), 0, base, theme)
			wantTrack := vaxis.ColorGray
			if rgb {
				wantTrack = vaxis.RGBColor(0x2b, 0x3a, 0x49)
			}
			for col, glyph := range []rune(tuiGauge(value, 10)) {
				style := renderedCellStyle(t, vx, col+2, 0)
				want := theme.usage(value).Foreground
				if glyph == '░' {
					want = wantTrack
				}
				if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
					want = theme.dim.Foreground
				}
				if style.Foreground != want || style.Background != base.Background || style.Attribute != base.Attribute {
					t.Errorf("rgb=%t value=%v cell=%d style=%+v want foreground=%v, base=%+v", rgb, value, col, style, want, base)
				}
			}
			if got := renderedCellStyle(t, vx, 0, 0); got != base {
				t.Errorf("literal track glyph changed style: %+v", got)
			}
			if got := renderedCellStyle(t, vx, 13, 0); got != base {
				t.Errorf("suffix changed style: %+v", got)
			}
			vx.Close()
		}
	}
}

func TestTUIGaugeLineClipsAndSanitizes(t *testing.T) {
	vx := newRenderTestVaxis(t, 8, 2)
	defer vx.Close()
	theme := newTUITheme(true).modal()
	line := tuiGaugeLine{prefix: "x\n", percent: 25, cells: 10, suffix: "\r\tend"}
	line.draw(vx.Window().New(0, 0, 6, 1), 0, theme.text, theme)
	if got := renderedCellGrapheme(t, vx, 0, 1); got != "" && got != " " {
		t.Fatalf("line escaped its row: %q", got)
	}
	if got := renderedCellGrapheme(t, vx, 5, 0); got != "…" {
		t.Fatalf("clipping marker=%q", got)
	}
	if got := renderedCellStyle(t, vx, 4, 0).Background; got != theme.raised.Background {
		t.Fatal("gauge lost modal background")
	}
}

func TestTUIGaugeSurfaces(t *testing.T) {
	for _, rgb := range []bool{false, true} {
		theme := newTUITheme(rgb)
		a, _ := newTestManager(t, 80, 24)
		used, total := uint64(75<<20), uint64(100<<20)
		value := 75.0
		record := tuiFleetRecord{Status: "running", Node: Node{ID: "gauges", LastRuntimeObservation: &RuntimeObservation{MemoryUsedBytes: &used, MemoryTotalBytes: &total, DiskUsedBytes: &used, DiskTotalBytes: &total}}}
		a.fleet.selectedID = record.Node.ID
		a.host = tuiHostSummary{AllocationsKnown: true, AllocatedCPUs: 3, AllocatedMemoryBytes: used, Capacity: hostinfo.Snapshot{CPUs: 4, MemoryBytes: &total}}
		latest := nodeUsageEvent{DiskUsedBytes: &used, DiskTotalBytes: &total, Details: &daemon.NodeUsageDetails{CPUCores: []daemon.CPUCoreUsage{{Index: 0, UsagePercent: &value}}}}
		surfaces := map[string]func(vaxis.Window){
			"fleet": func(win vaxis.Window) {
				a.drawFleetRecord(win, record, []tuiFleetColumn{{field: tuiFleetSortMemory, col: 2, width: 24}}, false, theme)
			},
			"overview": func(win vaxis.Window) { a.drawOverviewResources(win, record, theme) },
			"cpu":      func(win vaxis.Window) { a.drawMetricCPU(win, nil, latest, time.Now(), time.Minute, theme) },
			"disk":     func(win vaxis.Window) { drawMetricDisk(win, nil, latest, theme) },
			"host":     func(win vaxis.Window) { a.drawSystemHost(win, theme) },
			"create": func(win vaxis.Window) {
				for i, line := range createHostFit(a.host, Configuration{}) {
					line.draw(win, i, theme.modal().text, theme.modal())
				}
			},
		}
		for name, draw := range surfaces {
			t.Run(fmt.Sprintf("%s/rgb=%t", name, rgb), func(t *testing.T) {
				tuiFill(a.vx.Window(), theme.text)
				draw(a.vx.Window())
				tracks, filled := 0, 0
				for row := 0; row < 24; row++ {
					for col := 0; col < 80; col++ {
						glyph := renderedCellGrapheme(t, a.vx, col, row)
						if glyph != "░" && glyph != "█" {
							continue
						}
						style := renderedCellStyle(t, a.vx, col, row)
						want := theme.amber.Foreground
						if glyph == "░" {
							tracks++
							want = theme.track.Foreground
						} else {
							filled++
						}
						if style.Foreground != want {
							t.Fatalf("%s (%d,%d) foreground=%v want=%v", glyph, col, row, style.Foreground, want)
						}
						if name == "fleet" && (style.Background != theme.selected.Background || style.Attribute != theme.selected.Attribute) {
							t.Fatal("selected gauge lost row style")
						}
					}
				}
				if tracks == 0 || filled == 0 {
					t.Fatalf("missing gauge: filled=%d tracks=%d", filled, tracks)
				}
			})
		}
	}
}
