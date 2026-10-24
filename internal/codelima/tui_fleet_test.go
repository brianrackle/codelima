package codelima

import (
	"math"
	"slices"
	"testing"
)

func fleetTestRecords() []tuiFleetRecord {
	return []tuiFleetRecord{
		{Node: Node{ID: "a", Slug: "api-redesign", DirectoryPath: "/work/acme api", ConfigurationSlug: "medium"}, Status: "running", Ports: []int{3000, 5173}},
		{Node: Node{ID: "b", Slug: "api-clone", DirectoryPath: "/work/acme api", ConfigurationSlug: "medium"}, Status: "created"},
		{Node: Node{ID: "c", Slug: "codelima", DirectoryPath: "/work/codelima", ConfigurationSlug: "large"}, Status: "running"},
		{Node: Node{ID: "d", Slug: "lab", DirectoryPath: "/work/lab", ConfigurationSlug: "small"}, Status: "failed"},
	}
}

func fleetIDs(records []tuiFleetRecord) []string {
	ids := make([]string, len(records))
	for i, record := range records {
		ids[i] = record.Node.ID
	}
	return ids
}

func TestTUIFleetFilterSelectionAndMarks(t *testing.T) {
	fleet := newTUIFleet("")
	records := fleetTestRecords()
	fleet.replace(records)
	fleet.selectID("c")
	fleet.toggleMark()
	fleet.selectID("a")
	fleet.toggleMark()
	fleet.setFilter(`status:running template:medium dir:"acme api" port:5173 redesign`)
	if got := fleetIDs(fleet.visible); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("compound filter = %v", got)
	}
	if got := fleetIDs(fleet.targets()); !slices.Equal(got, []string{"a", "c"}) {
		t.Fatalf("hidden marks lost from batch: %v", got)
	}
	fleet.setFilter("no-match")
	if fleet.selectedID != "" || len(fleet.visible) != 0 {
		t.Fatal("empty filter results retain a hidden selection")
	}
	if len(fleet.targets()) != 2 {
		t.Fatal("filtering discarded batch marks")
	}
	fleet.setFilter("")
	fleet.selectID("c")
	fleet.replace(append(records[2:], records[:2]...))
	if fleet.selectedID != "c" {
		t.Fatal("refresh moved selection to a different identity")
	}
	fleet.replace(records[:2])
	if len(fleet.marks) != 1 || !fleet.marks["a"] {
		t.Fatalf("marks not reconciled after deletion: %v", fleet.marks)
	}
}

func TestTUIFleetMetricSortKeepsMissingSamplesLastAndCursorStable(t *testing.T) {
	records := fleetTestRecords()
	for i, cpu := range []float64{42, math.NaN(), 71, math.Inf(1)} {
		records[i].Node.LastRuntimeObservation = &RuntimeObservation{Status: ObservationRunning, CPUUsagePercent: &cpu}
	}
	fleet := newTUIFleet("")
	fleet.replace(records)
	fleet.selectID("a")
	fleet.sort = tuiFleetSortCPU
	fleet.descending = true
	fleet.rebuild()
	if got := fleetIDs(fleet.visible); !slices.Equal(got, []string{"c", "a", "b", "d"}) {
		t.Fatalf("descending CPU order = %v", got)
	}
	if fleet.grouped() || fleet.selectedID != "a" {
		t.Fatal("metric sort must be flat without changing selected identity")
	}
	fleet.descending = false
	fleet.rebuild()
	if got := fleetIDs(fleet.visible); !slices.Equal(got, []string{"a", "c", "b", "d"}) {
		t.Fatalf("ascending CPU order = %v", got)
	}
	fleet.sort = tuiFleetSortName
	fleet.rebuild()
	if !fleet.grouped() {
		t.Fatal("returning to name sort lost grouping preference")
	}
}

func TestTUIFleetGroupingAndViewportAreNodeAware(t *testing.T) {
	fleet := newTUIFleet("")
	fleet.replace(fleetTestRecords())
	if len(fleet.rows) != 7 || fleet.rows[0].Directory != "/work/acme api" || fleet.rows[0].Count != 2 {
		t.Fatalf("group rows = %+v", fleet.rows)
	}
	fleet.selectID("b")
	fleet.move(1)
	if fleet.selectedID != "a" {
		t.Fatal("navigation selected a group heading or skipped a node")
	}
	fleet.move(1)
	if fleet.selectedID != "c" {
		t.Fatal("navigation did not cross the directory heading")
	}
	rows := fleet.viewport(2)
	if len(rows) != 2 || rows[0].Directory != "/work/codelima" || rows[1].NodeID != "c" {
		t.Fatalf("selected row lost group context: %+v", rows)
	}
	if got := fleet.viewport(1); len(got) != 1 || got[0].NodeID != "c" {
		t.Fatalf("single-line viewport lost selected node: %+v", got)
	}
	fleet.markVisible()
	if len(fleet.targets()) != 4 {
		t.Fatal("mark all did not cover every displayed node")
	}
	fleet.clearMarks()
	if got := fleetIDs(fleet.targets()); !slices.Equal(got, []string{"c"}) {
		t.Fatalf("unmarked action target = %v", got)
	}
	scoped := newTUIFleet("/work")
	if scoped.grouped() {
		t.Fatal("scoped launch should start without directory grouping")
	}
}

func TestTUIFleetFilterHandlesPartialInputAndLiteralPunctuation(t *testing.T) {
	fleet := newTUIFleet("")
	fleet.replace(fleetTestRecords())
	for _, filter := range []string{`DIR:"acme api`, `dir:acme\ api`, "ACME API", `status:RUNNING port:3000`} {
		fleet.setFilter(filter)
		if !slices.Contains(fleetIDs(fleet.visible), "a") {
			t.Errorf("live filter %q lost matching node", filter)
		}
	}
	fleet.setFilter("port:300")
	if len(fleet.visible) != 0 {
		t.Fatal("port filter matched a substring of a different port")
	}
}

func TestTUIFleetLayoutAdaptsWithoutOverlappingChrome(t *testing.T) {
	for _, width := range []int{60, 80, 99, 100, 139, 140, 180} {
		for _, height := range []int{14, 24, 38, 60} {
			for _, count := range []int{0, 1, 11, 200} {
				layout := layoutTUIFleet(width, height, count)
				if layout.Compact != (width < 100) {
					t.Fatalf("wrong density at %dx%d", width, height)
				}
				if layout.Table.row < 2 || layout.Table.height < 4 || layout.Table.row+layout.Table.height > height-3 {
					t.Fatalf("table overlaps chrome at %dx%d/%d: %+v", width, height, count, layout)
				}
				if layout.Preview.height > 0 && (width < 140 || layout.Preview.row < layout.Table.row+layout.Table.height || layout.Preview.row+layout.Preview.height > height-2) {
					t.Fatalf("invalid preview at %dx%d: %+v", width, height, layout)
				}
			}
		}
	}
	if layout := layoutTUIFleet(150, 38, 11); layout.Preview.height < 10 {
		t.Fatalf("reference-size dashboard lacks preview: %+v", layout)
	}
}
