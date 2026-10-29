package codelima

import (
	"cmp"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// tuiFleetRecord joins observations at the frontend boundary. Missing metrics
// remain missing; the projection never turns absence into a zero observation.
type tuiFleetRecord struct {
	Node             Node
	Status           string
	Setup            string
	Sessions         int
	Ports            []int
	PortsUnavailable bool
	StartedAt        time.Time
}

type tuiFleetSort int

const (
	tuiFleetSortName tuiFleetSort = iota
	tuiFleetSortStatus
	tuiFleetSortSetup
	tuiFleetSortTemplate
	tuiFleetSortCPU
	tuiFleetSortMemory
	tuiFleetSortDisk
	tuiFleetSortSessions
	tuiFleetSortPorts
	tuiFleetSortUptime
	tuiFleetSortCount
)

// Directory is a display column in the flat projection, not a sort-cycle key.
const tuiFleetSortDirectory = tuiFleetSortCount

func (s tuiFleetSort) String() string {
	names := [...]string{"name", "status", "setup", "template", "cpu", "memory", "disk", "sessions", "ports", "uptime"}
	if s < 0 || s >= tuiFleetSortCount {
		return "name"
	}
	return names[s]
}

type tuiFleetRow struct {
	NodeID    string
	Directory string
	Count     int
}

// Selection and marks use durable IDs. Row indices belong only to the current
// projection and must never be retained across a refresh, filter or sort.
type tuiFleet struct {
	records    []tuiFleetRecord
	visible    []tuiFleetRecord
	rows       []tuiFleetRow
	selectedID string
	marks      map[string]bool
	filter     string
	sort       tuiFleetSort
	descending bool
	groupByDir bool
	scroll     int
}

func newTUIFleet(scope string) *tuiFleet {
	return &tuiFleet{marks: make(map[string]bool), groupByDir: scope == ""}
}

func (f *tuiFleet) replace(records []tuiFleetRecord) {
	f.records = slices.Clone(records)
	ids := make(map[string]bool, len(records))
	for _, record := range records {
		ids[record.Node.ID] = true
	}
	for id := range f.marks {
		if !ids[id] {
			delete(f.marks, id)
		}
	}
	f.rebuild()
}

func (f *tuiFleet) grouped() bool {
	return f.groupByDir && f.sort < tuiFleetSortCPU
}

func (f *tuiFleet) setFilter(filter string) {
	f.filter = filter
	f.scroll = 0
	f.rebuild()
}

func (f *tuiFleet) rebuild() {
	terms := tuiFleetFilterTerms(f.filter)
	f.visible = f.visible[:0]
	for _, record := range f.records {
		if tuiFleetMatches(record, terms) {
			f.visible = append(f.visible, record)
		}
	}
	slices.SortFunc(f.visible, func(a, b tuiFleetRecord) int {
		if f.grouped() {
			if order := cmp.Compare(a.Node.DirectoryPath, b.Node.DirectoryPath); order != 0 {
				return order
			}
		}
		return f.compare(a, b)
	})
	f.rows = f.rows[:0]
	selected := false
	group := -1
	for i, record := range f.visible {
		if f.grouped() && (i == 0 || record.Node.DirectoryPath != f.visible[i-1].Node.DirectoryPath) {
			group = len(f.rows)
			f.rows = append(f.rows, tuiFleetRow{Directory: record.Node.DirectoryPath})
		}
		if group >= 0 {
			f.rows[group].Count++
		}
		f.rows = append(f.rows, tuiFleetRow{NodeID: record.Node.ID})
		selected = selected || record.Node.ID == f.selectedID
	}
	if !selected {
		f.selectedID = ""
		if len(f.visible) > 0 {
			f.selectedID = f.visible[0].Node.ID
		}
	}
}

func (f *tuiFleet) compare(a, b tuiFleetRecord) int {
	order := 0
	switch f.sort {
	case tuiFleetSortName:
		order = strings.Compare(strings.ToLower(a.Node.Slug), strings.ToLower(b.Node.Slug))
	case tuiFleetSortStatus:
		order = strings.Compare(a.Status, b.Status)
	case tuiFleetSortSetup:
		order = strings.Compare(a.Setup, b.Setup)
	case tuiFleetSortTemplate:
		order = strings.Compare(a.Node.ConfigurationSlug, b.Node.ConfigurationSlug)
	default:
		av, aok := tuiFleetMetric(a, f.sort)
		bv, bok := tuiFleetMetric(b, f.sort)
		if aok != bok {
			if aok {
				return -1
			}
			return 1
		}
		order = cmp.Compare(av, bv)
	}
	if order != 0 {
		if f.descending {
			return -order
		}
		return order
	}
	return strings.Compare(a.Node.ID, b.Node.ID)
}

func tuiFleetMetric(record tuiFleetRecord, field tuiFleetSort) (float64, bool) {
	switch field {
	case tuiFleetSortSessions:
		return float64(record.Sessions), true
	case tuiFleetSortPorts:
		return float64(len(record.Ports)), !record.PortsUnavailable
	case tuiFleetSortUptime:
		// Older starts have greater uptime without a changing clock in SortFunc.
		return -float64(record.StartedAt.Unix()), !record.StartedAt.IsZero()
	}
	obs := record.Node.LastRuntimeObservation
	if obs == nil {
		return 0, false
	}
	switch field {
	case tuiFleetSortCPU:
		if obs.CPUUsagePercent != nil {
			value := *obs.CPUUsagePercent
			if !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 {
				return value, true
			}
		}
	case tuiFleetSortMemory:
		return tuiFleetUsagePercent(obs.MemoryUsedBytes, obs.MemoryTotalBytes)
	case tuiFleetSortDisk:
		return tuiFleetUsagePercent(obs.DiskUsedBytes, obs.DiskTotalBytes)
	}
	return 0, false
}

func tuiFleetUsagePercent(used, total *uint64) (float64, bool) {
	if used == nil || total == nil || *total == 0 {
		return 0, false
	}
	return 100 * float64(*used) / float64(*total), true
}

// The lexer tolerates unfinished quotes while typing, and allows escaped spaces
// in directory names. An unknown prefix stays a literal free-text term.
func tuiFleetFilterTerms(input string) []string {
	var terms []string
	var term strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if term.Len() > 0 {
			terms = append(terms, strings.ToLower(term.String()))
			term.Reset()
		}
	}
	for _, r := range input {
		switch {
		case escaped:
			term.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				term.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case unicode.IsSpace(r):
			flush()
		default:
			term.WriteRune(r)
		}
	}
	if escaped {
		term.WriteRune('\\')
	}
	flush()
	return terms
}

func tuiFleetMatches(record tuiFleetRecord, terms []string) bool {
	for _, term := range terms {
		field, value, typed := strings.Cut(term, ":")
		text := record.Node.Slug + " " + record.Node.DirectoryPath
		if typed {
			switch field {
			case "status":
				text = record.Status
			case "setup":
				text = record.Setup
			case "template":
				text = record.Node.ConfigurationSlug
			case "dir":
				text = record.Node.DirectoryPath
			case "port":
				port, err := strconv.Atoi(value)
				if err != nil || !slices.Contains(record.Ports, port) {
					return false
				}
				continue
			default:
				value = term
			}
		} else {
			value = term
		}
		if !strings.Contains(strings.ToLower(text), value) {
			return false
		}
	}
	return true
}

func (f *tuiFleet) selectID(id string) {
	for _, record := range f.visible {
		if record.Node.ID == id {
			f.selectedID = id
			return
		}
	}
}

func (f *tuiFleet) move(delta int) {
	if len(f.visible) == 0 {
		return
	}
	index := slices.IndexFunc(f.visible, func(record tuiFleetRecord) bool { return record.Node.ID == f.selectedID })
	index = max(0, min(len(f.visible)-1, index+delta))
	f.selectedID = f.visible[index].Node.ID
}

func (f *tuiFleet) toggleMark() {
	if f.selectedID == "" {
		return
	}
	if f.marks[f.selectedID] {
		delete(f.marks, f.selectedID)
	} else {
		f.marks[f.selectedID] = true
	}
}

func (f *tuiFleet) markVisible() {
	for _, record := range f.visible {
		f.marks[record.Node.ID] = true
	}
}

func (f *tuiFleet) clearMarks() { clear(f.marks) }

func (f *tuiFleet) targets() []tuiFleetRecord {
	var targets []tuiFleetRecord
	for _, record := range f.records {
		if f.marks[record.Node.ID] || (len(f.marks) == 0 && record.Node.ID == f.selectedID) {
			targets = append(targets, record)
		}
	}
	return targets
}

func (f *tuiFleet) viewport(height int) []tuiFleetRow {
	if height <= 0 || len(f.rows) == 0 {
		return nil
	}
	selected := slices.IndexFunc(f.rows, func(row tuiFleetRow) bool { return row.NodeID != "" && row.NodeID == f.selectedID })
	f.scroll = max(0, min(f.scroll, len(f.rows)-height))
	if selected >= 0 {
		if selected < f.scroll {
			f.scroll = selected
		}
		if selected >= f.scroll+height {
			f.scroll = selected - height + 1
		}
		if height > 1 && selected == f.scroll && selected > 0 && f.rows[selected-1].NodeID == "" {
			f.scroll--
		}
	}
	return f.rows[f.scroll:min(len(f.rows), f.scroll+height)]
}

type tuiFleetLayout struct {
	Compact bool
	Table   tuiRect
	Preview tuiRect
	Summary tuiRect
}

func layoutTUIFleet(width, height, rows int) tuiFleetLayout {
	layout := tuiFleetLayout{Compact: width < 100}
	// The header, counts, status and footer remain outside the scrollable body.
	available := max(0, height-5)
	tableHeight := min(available, max(4, rows+3))
	if width >= 140 && available >= 18 {
		tableHeight = min(tableHeight, available-min(16, available-4))
		layout.Preview = tuiRect{col: 1, row: 3 + tableHeight, width: max(0, width-2), height: available - tableHeight - 1}
	} else {
		// Leave the selected/marked summary above the filter and status rows,
		// even when the table needs to scroll through a large fleet.
		tableHeight = min(tableHeight, max(0, height-7))
	}
	layout.Table = tuiRect{col: 1, row: 2, width: max(0, width-2), height: tableHeight}
	layout.Summary = tuiRect{col: 2, row: 2 + tableHeight, width: max(0, width-4), height: 1}
	if layout.Preview.height == 0 {
		layout.Summary.row++
	}
	return layout
}
