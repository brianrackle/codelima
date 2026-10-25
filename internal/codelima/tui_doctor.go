package codelima

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"go.rockorager.dev/vaxis"
)

const tuiDoctorInterval = time.Minute

type tuiDoctorState struct {
	report               DoctorReport
	checkedAt, startedAt time.Time
	err                  error
	generation           uint64
	loading, repairing   bool
	queuedRepair         bool
	cancel               context.CancelFunc
	index, scroll        int
	controls             []tuiDoctorControl
}

type tuiDoctorDataEvent struct {
	generation uint64
	checkedAt  time.Time
	report     DoctorReport
	err        error
	repair     bool
	repairErr  error
	repaired   DoctorReport
}

type tuiDoctorRow struct{ key, name, message, status string }
type tuiDoctorControl struct {
	rect   tuiRect
	action string
	index  int
}

// Doctor is global state: navigating away cannot discard findings or a repair
// completion. Only one worker can run, and periodic checks never repair.
func (a *vaxisTUIApp) startDoctorCheck(repair, force bool) {
	if a.service == nil {
		return
	}
	s := &a.doctor
	if s.loading {
		if repair && !s.repairing {
			s.queuedRepair = true
		}
		return
	}
	if !force && time.Since(s.startedAt) < tuiDoctorInterval {
		return
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := 15 * time.Second
	if repair {
		timeout = time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	s.generation++
	s.cancel, s.loading, s.repairing, s.startedAt = cancel, true, repair, time.Now()
	generation, service := s.generation, a.service
	load := func() tuiDoctorDataEvent {
		defer cancel()
		e := tuiDoctorDataEvent{generation: generation, repair: repair}
		if repair {
			e.repaired, e.repairErr = service.Doctor(ctx, true)
		}
		// Repair messages describe completed actions, not open findings.
		// Rechecking also observes partial repairs if a later step failed.
		e.report, e.err = service.Doctor(ctx, false)
		if ctx.Err() != nil {
			e.err = ctx.Err()
		}
		e.checkedAt = time.Now()
		return e
	}
	if a.postEvent == nil {
		a.applyDoctorData(load())
		return
	}
	go func() { a.postCompletion(load()) }()
}

func (a *vaxisTUIApp) cancelDoctorCheck() {
	s := &a.doctor
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.generation++
	s.loading, s.queuedRepair = false, false
}

func (a *vaxisTUIApp) applyDoctorData(e tuiDoctorDataEvent) {
	s := &a.doctor
	if e.generation != s.generation {
		return
	}
	selected := ""
	if rows := a.doctorRows(); s.index >= 0 && s.index < len(rows) {
		selected = rows[s.index].key
	}
	s.loading, s.repairing, s.cancel, s.err = false, false, nil, e.err
	if e.err == nil {
		s.report, s.checkedAt = e.report, e.checkedAt
		rows := a.doctorRows()
		s.index = slices.IndexFunc(rows, func(row tuiDoctorRow) bool { return row.key == selected })
		if s.index < 0 {
			s.index = max(0, slices.IndexFunc(rows, func(row tuiDoctorRow) bool { return row.status != "ok" }))
		}
	}
	if e.repair {
		for _, warning := range e.repaired.Warnings {
			a.setStatus(slog.LevelInfo, "Doctor repair: "+warning)
		}
		switch {
		case e.repairErr != nil:
			a.setStatus(slog.LevelError, "Doctor repair: "+e.repairErr.Error())
		case e.err != nil:
			a.setStatus(slog.LevelWarn, "Doctor repair completed · re-check unavailable: "+e.err.Error())
		default:
			a.setStatus(slog.LevelInfo, fmt.Sprintf("Doctor repair completed · %d open findings after re-check", a.doctorIssueCount()))
		}
		a.startDataRefresh(a.state.selectedEntry().key())
	}
	if s.queuedRepair {
		s.queuedRepair = false
		a.startDoctorCheck(true, true)
	}
}

func (a *vaxisTUIApp) doctorRows() []tuiDoctorRow {
	rows := make([]tuiDoctorRow, 0, len(a.doctor.report.Checks)+len(a.doctor.report.Warnings)+3)
	for _, c := range a.doctor.report.Checks {
		rows = append(rows, tuiDoctorRow{"check:" + c.Name, c.Name, c.Message, c.Status})
	}
	for _, warning := range a.doctor.report.Warnings {
		rows = append(rows, tuiDoctorRow{"warning:" + warning, "", warning, "warn"})
	}
	h := a.host
	if !h.AllocationsKnown {
		if !h.Capacity.ObservedAt.IsZero() {
			rows = append(rows, tuiDoctorRow{"host:allocation", "VM allocation", "Runtime observations unavailable; capacity use is unknown.", "warn"})
		}
		return rows
	}
	if h.Capacity.CPUs > 0 && h.AllocatedCPUs > uint64(h.Capacity.CPUs) {
		rows = append(rows, tuiDoctorRow{"host:cpu", "vCPU overcommitted", fmt.Sprintf("%d / %d · stop a node, or accept", h.AllocatedCPUs, h.Capacity.CPUs), "warn"})
	}
	if h.Capacity.MemoryBytes != nil && h.AllocatedMemoryBytes > *h.Capacity.MemoryBytes {
		rows = append(rows, tuiDoctorRow{"host:memory", "Memory overcommitted", fmt.Sprintf("%s / %s · stop a node, or accept", tuiHostBytes(&h.AllocatedMemoryBytes), tuiHostBytes(h.Capacity.MemoryBytes)), "warn"})
	}
	if h.UnknownNodes > 0 {
		rows = append(rows, tuiDoctorRow{"host:unknown", "VM allocation incomplete", countLabel(h.UnknownNodes, "node has no runtime observation", "nodes have no runtime observation"), "warn"})
	}
	return rows
}

func (a *vaxisTUIApp) doctorIssueCount() int {
	count := 0
	for _, row := range a.doctorRows() {
		if row.status != "ok" {
			count++
		}
	}
	return count
}

func (a *vaxisTUIApp) doctorBadge() string {
	s := &a.doctor
	count := a.doctorIssueCount()
	if s.checkedAt.IsZero() || s.err != nil {
		if count > 0 {
			return fmt.Sprintf("⚠%d?", count)
		}
		return "⚠?"
	}
	if count > 0 {
		return fmt.Sprintf("⚠%d", count)
	}
	return ""
}

func (a *vaxisTUIApp) moveDoctorSelection(delta int) {
	s := &a.doctor
	s.index = max(0, min(s.index+delta, len(a.doctorRows())-1))
}

func (a *vaxisTUIApp) openDoctorDetails() {
	rows := a.doctorRows()
	title, text := "Doctor", ""
	if a.doctor.err != nil {
		text = "Last check failed: " + a.doctor.err.Error() + "\n\n"
	}
	if a.doctor.index >= 0 && a.doctor.index < len(rows) {
		row := rows[a.doctor.index]
		title += " · " + coalesce(row.name, "Warning")
		text += row.status + "\n\n" + row.message
	}
	if text != "" {
		a.overlay = &tuiTextView{title: title, text: text}
	}
}

func (a *vaxisTUIApp) handleDoctorMouse(mouse vaxis.Mouse) bool {
	if a.view != tuiViewSystem || mouse.EventType != vaxis.EventPress || mouse.Button != vaxis.MouseLeftButton {
		return false
	}
	for _, c := range a.doctor.controls {
		if !c.rect.contains(mouse.Col, mouse.Row) {
			continue
		}
		a.systemPanel = tuiSystemDoctor
		switch c.action {
		case "select":
			a.doctor.index = c.index
		case "details":
			a.openDoctorDetails()
		case "check":
			a.startDoctorCheck(false, true)
		case "repair":
			a.startDoctorCheck(true, true)
		}
		return true
	}
	return false
}

func (a *vaxisTUIApp) drawDoctor(win vaxis.Window, t tuiTheme) {
	s := &a.doctor
	w, h := win.Size()
	s.controls = nil
	title := "Doctor · not checked"
	if !s.checkedAt.IsZero() {
		title = fmt.Sprintf("Doctor · checked %s · %d open", s.checkedAt.Local().Format("15:04:05"), a.doctorIssueCount())
	}
	tuiLine(win, 0, title, t.muted)
	status, style := a.keyHints("{system.repair} repairs metadata; unreadable records move to _quarantine/."), t.dim
	switch {
	case s.queuedRepair:
		status = "Checking… · repair queued"
	case s.repairing:
		status = "Repairing metadata…"
	case s.loading:
		status = "Checking system…"
	case s.err != nil:
		status, style = "Last check failed: "+s.err.Error()+" · showing last check", t.coral
	}
	tuiLine(win, 1, status, style)
	rows := a.doctorRows()
	visible := max(1, h-4)
	s.index = max(0, min(s.index, len(rows)-1))
	if s.index < s.scroll {
		s.scroll = s.index
	}
	if s.index >= s.scroll+visible {
		s.scroll = s.index - visible + 1
	}
	s.scroll = max(0, min(s.scroll, len(rows)-visible))
	for i := s.scroll; i < min(len(rows), s.scroll+visible); i++ {
		row := rows[i]
		glyph, style := "✓", t.mint
		if row.status != "ok" {
			glyph, style = "⚠", t.amber
			if row.status == "fail" {
				glyph, style = "✕", t.coral
			}
		}
		if i == s.index && a.systemPanel == tuiSystemDoctor {
			style.Background = t.selected.Background
			style.Attribute |= t.selected.Attribute
		}
		line := win.New(0, 2+i-s.scroll, w, 1)
		tuiFill(line, style)
		text := strings.TrimSpace(row.name + " " + row.message)
		tuiLine(line, 0, glyph+" "+plainLogText(text), style)
		x, y := line.Origin()
		s.controls = append(s.controls, tuiDoctorControl{tuiRect{col: x, row: y, width: w, height: 1}, "select", i})
	}
	col := 0
	for _, action := range [][2]string{{"details", a.keyHints("{system.open} details")}, {"check", a.keyHints("{system.refresh} re-check")}, {"repair", a.keyHints("{system.repair} repair")}} {
		text := action[1] + "  "
		width := len(text)
		if col+width > w {
			break
		}
		button := win.New(col, h-1, width, 1)
		tuiLine(button, 0, text, t.mint)
		x, y := button.Origin()
		s.controls = append(s.controls, tuiDoctorControl{tuiRect{col: x, row: y, width: width, height: 1}, action[0], 0})
		col += width
	}
	if col+10 < w && len(rows) > 0 {
		tuiLine(win.New(col, h-1, w-col, 1), 0, fmt.Sprintf("%d/%d", s.index+1, len(rows)), t.dim)
	}
}
