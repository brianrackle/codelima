package codelima

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/border"
)

func (a *vaxisTUIApp) drawPublishedPorts(win vaxis.Window, theme tuiTheme) {
	w, h := win.Size()
	if w < 30 || h < 12 {
		tuiLine(win.New(1, 2, w-2, 1), 0, "Ports · enlarge the terminal", theme.muted)
		return
	}
	s := &a.ports
	s.mouseRows, s.controls = nil, nil
	a.reconcilePortSelection()
	rows := a.portRows()
	header := "HTTP + WebSocket by hostname · raw TCP needs a published port"
	if s.query != "" || s.input != nil {
		header = "/ " + s.query
	}
	tuiLine(win.New(2, 1, w-4, 1), 0, header, theme.dim)
	rowHeight := 1
	if w < 120 {
		rowHeight = 2
	}
	tableHeight := min(max(6, len(rows)*rowHeight+3), max(6, h/2-3))
	outer := win.New(1, 2, w-2, tableHeight)
	box := border.All(outer, theme.mint)
	title := fmt.Sprintf(" Ports · %s · %s · port ▲ ", countLabel(len(s.snapshot.Rows), "route", "routes"), countLabel(len(s.snapshot.Nodes), "node", "nodes"))
	if w >= 120 {
		title = fmt.Sprintf(" Ports · %s on %s · discovery %ds · port ▲ ", countLabel(len(s.snapshot.Rows), "route", "routes"), countLabel(len(s.snapshot.Nodes), "node", "nodes"), s.snapshot.IntervalSeconds)
	}
	tuiLine(outer.New(2, 0, w-6, 1), 0, title, theme.mint)
	bodyWidth, _ := box.Size()
	drawPortTableRow(box.New(0, 0, bodyWidth, 1), daemon.PortRoute{}, false, true, theme)
	visible := max(1, (tableHeight-3)/rowHeight)
	index := slices.IndexFunc(rows, func(row daemon.PortRoute) bool { return row.ID == s.selected })
	if index >= 0 {
		s.scroll = min(s.scroll, index)
		s.scroll = max(s.scroll, index-visible+1)
	}
	s.scroll = max(0, min(s.scroll, len(rows)-visible))
	col, top := box.Origin()
	for i := s.scroll; i < min(len(rows), s.scroll+visible); i++ {
		y := 1 + (i-s.scroll)*rowHeight
		drawPortTableRow(box.New(0, y, bodyWidth, rowHeight), rows[i], rows[i].ID == s.selected, false, theme)
		s.mouseRows = append(s.mouseRows, tuiPortMouseRow{id: rows[i].ID, rect: tuiRect{col: col, row: top + y, width: bodyWidth, height: rowHeight}})
	}
	if len(rows) == 0 {
		message := "No matching routes."
		if !s.loaded {
			message = "Loading routing state…"
		}
		if a.service == nil || a.service.daemonClient == nil {
			message = "Connect to the daemon to inspect routing."
		}
		if s.loaded && !s.snapshot.Enabled {
			message = "Port forwarding is unavailable for this runtime."
		}
		tuiLine(box.New(1, 2, bodyWidth-2, 1), 0, message, theme.dim)
	}
	detailY, detailWidth := tableHeight+3, w-2
	if w >= 120 {
		detailWidth = w - 52
	}
	detailHeight := max(3, h-detailY-5)
	detailOuter := win.New(1, detailY, detailWidth, detailHeight)
	detail := border.All(detailOuter, theme.border)
	tuiLine(detailOuter.New(2, 0, detailWidth-4, 1), 0, " Selected route · PgUp/PgDn details ", theme.muted)
	nodeLines := a.portNodeLines()
	includeNodes := w < 120 || len(nodeLines) > detailHeight-2
	if !includeNodes {
		for _, line := range nodeLines {
			if len(commandVisualRows(vaxis.Characters(line), max(1, w-detailWidth-7))) > 1 {
				includeNodes = true
				break
			}
		}
	}
	lines := a.portDetailLines(includeNodes)
	innerWidth, innerHeight := detail.Size()
	var wrapped []string
	for _, line := range lines {
		for _, row := range commandVisualRows(vaxis.Characters(plainLogText(line)), max(1, innerWidth-2)) {
			wrapped = append(wrapped, row.text)
		}
	}
	s.detailHeight = max(1, innerHeight)
	s.detailScroll = max(0, min(s.detailScroll, len(wrapped)-s.detailHeight))
	for i, line := range wrapped[s.detailScroll:min(len(wrapped), s.detailScroll+s.detailHeight)] {
		tuiLine(detail.New(1, i, innerWidth-2, 1), 0, line, theme.text)
	}
	if w >= 120 {
		nodeOuter := win.New(detailWidth+2, detailY, w-detailWidth-3, detailHeight)
		nodeBox := border.All(nodeOuter, theme.border)
		tuiLine(nodeOuter.New(2, 0, w-detailWidth-7, 1), 0, " By node ", theme.muted)
		nw, nh := nodeBox.Size()
		for i, line := range nodeLines {
			if i >= nh {
				break
			}
			tuiLine(nodeBox.New(1, i, nw-2, 1), 0, line, theme.muted)
		}
	}
	controls := []struct{ label, key string }{{"o Open", "o"}, {"y Copy", "y"}, {"p Pin", "p"}, {"Enter Node", "Enter"}, {"/ Filter", "/"}, {"C Refresh", "C"}}
	x := 2
	for _, control := range controls {
		label := "[" + control.label + "]"
		if x+len(label) > w-2 {
			break
		}
		tuiLine(win.New(x, h-4, len(label), 1), 0, label, theme.mint)
		key := vaxis.Key{Keycode: []rune(control.key)[0], Text: control.key}
		if control.key == "Enter" {
			key = vaxis.Key{Keycode: vaxis.KeyEnter}
		}
		s.controls = append(s.controls, tuiPortControl{key: key, rect: tuiRect{col: x, row: h - 4, width: len(label), height: 1}})
		x += len(label) + 1
	}
	status, style := fmt.Sprintf("%d shown · %d host listeners · live", len(rows), s.snapshot.Listeners), theme.dim
	switch {
	case a.daemonDisconnected:
		status, style = "Daemon disconnected · showing last observed routes", theme.amber
	case s.err != nil:
		status, style = "Routing unavailable · C retry · "+plainLogText(s.err.Error()), theme.coral
	case s.snapshot.InventoryError != "":
		status, style = "Inventory unavailable · retaining last observed routes", theme.amber
	case s.snapshot.PolicyError != "":
		status, style = "Routing preferences unavailable · "+plainLogText(s.snapshot.PolicyError), theme.coral
	case s.pinning:
		status = "Saving bare localhost owner…"
	case !s.loaded:
		status = "Waiting for routing state"
	case !s.snapshot.Enabled:
		status = "Port forwarding unavailable"
	}
	tuiLine(win.New(2, h-3, w-4, 1), 0, status, style)
}

func drawPortTableRow(win vaxis.Window, row daemon.PortRoute, selected, header bool, theme tuiTheme) {
	w, _ := win.Size()
	if selected {
		for _, style := range []*vaxis.Style{&theme.text, &theme.dim, &theme.muted, &theme.mint, &theme.amber, &theme.coral} {
			style.Background, style.Attribute = theme.selected.Background, theme.selected.Attribute
		}
		tuiFill(win, theme.text)
		tuiLine(win.New(0, 0, 2, 1), 0, "▸", theme.mint)
	}
	port, node, process, address, owner, status := fmt.Sprint(row.Port), plainLogText(row.NodeSlug), coalesce(plainLogText(row.Process), "—"), row.Address, "— shared", portStatusText(row)
	if row.Kind == "tcp" {
		owner = "published at create"
		process = coalesce(plainLogText(row.Process), "tcp")
	}
	if row.OwnerID == row.NodeID && row.OwnerID != "" {
		owner = "✓ " + row.OwnerReason
	}
	if row.Status == "conflicted" {
		owner = "unavailable"
	}
	if header {
		port, node, process, address, owner, status = "PORT", "NODE", "PROCESS", "ADDRESS", "BARE localhost:PORT", "STATUS"
	}
	style := theme.text
	if row.Status == "retrying" || row.Status == "legacy" {
		style = theme.amber
	}
	if row.Status == "conflicted" {
		style = theme.coral
	}
	if row.Status == "held" || row.Status == "stopped" || header {
		style = theme.dim
	}
	if w < 116 {
		tuiLine(win.New(2, 0, 6, 1), 0, port, theme.mint)
		tuiLine(win.New(9, 0, 21, 1), 0, node, style)
		tuiLine(win.New(31, 0, 12, 1), 0, process, theme.muted)
		tuiLine(win.New(44, 0, w-44, 1), 0, status, style)
		if !header {
			tuiLine(win.New(2, 1, w-3, 1), 0, address, style)
		}
		return
	}
	addressWidth := max(28, w-95)
	values := []struct {
		x, width int
		text     string
	}{{2, 6, port}, {9, 17, node}, {27, 10, process}, {38, addressWidth, address}, {39 + addressWidth, 21, owner}, {61 + addressWidth, w - 61 - addressWidth, status}}
	for _, value := range values {
		tuiLine(win.New(value.x, 0, value.width, 1), 0, value.text, style)
	}
}

func portStatusText(row daemon.PortRoute) string {
	switch row.Status {
	case "live":
		return "● live"
	case "retrying":
		text := "◐ retrying"
		if row.Kind == "http" {
			text = "◐ 502 · retrying"
		}
		if remaining := time.Until(row.RetryAt).Round(time.Second); remaining > 0 {
			text += " " + remaining.String()
		}
		return text
	case "held":
		return "○ held until ready"
	case "stopped":
		return "○ node stopped"
	case "legacy":
		return "◐ legacy · restart needed"
	case "conflicted":
		return "! port conflict"
	default:
		return row.Status
	}
}

func (a *vaxisTUIApp) portDetailLines(includeNodes bool) []string {
	var lines []string
	if a.ports.err != nil {
		lines = append(lines, a.ports.err.Error())
	}
	for _, message := range []string{a.ports.snapshot.InventoryError, a.ports.snapshot.PolicyError} {
		if message != "" {
			lines = append(lines, message)
		}
	}
	row, ok := a.portSelection()
	if !ok {
		lines = append(lines, "Select a route to inspect its owner and addresses.")
		if includeNodes {
			lines = append(lines, a.portNodeLines()...)
		}
		return lines
	}
	lines = append(lines, fmt.Sprintf(":%d on %s · %s", row.Port, row.NodeSlug, portStatusText(row)), row.Address)
	if row.Kind == "tcp" {
		lines = append(lines, fmt.Sprintf("Host :%d → guest :%d · raw TCP", row.Port, row.GuestPort), "Published mappings reserve the whole host port.")
	} else {
		owner := "unavailable"
		if row.OwnerID != "" {
			owner = row.OwnerSlug + " (" + row.OwnerReason + ")"
		}
		lines = append(lines, fmt.Sprintf("localhost:%d → %s", row.Port, owner))
		for _, candidate := range a.ports.snapshot.Rows {
			if candidate.Kind == "http" && candidate.Port == row.Port {
				lines = append(lines, candidate.Address+" → "+candidate.NodeSlug+" · "+portStatusText(candidate))
			}
		}
		lines = append(lines, "p pins bare localhost to this node until its listener stops.", "$ curl "+row.Address, "Saved ownership survives daemon restarts; live connections reconnect.")
	}
	if row.Error != "" {
		lines = append(lines, row.Error)
	}
	if includeNodes {
		lines = append(lines, "", "By node")
		lines = append(lines, a.portNodeLines()...)
	}
	return lines
}

func (a *vaxisTUIApp) portNodeLines() []string {
	var result []string
	for _, node := range a.ports.snapshot.Nodes {
		var ports []string
		for _, row := range a.ports.snapshot.Rows {
			if row.NodeID == node.ID {
				text := fmt.Sprint(row.Port)
				if row.Status != "live" {
					text += " (" + row.Status + ")"
				}
				ports = append(ports, text)
			}
		}
		value := strings.Join(ports, " ")
		if value == "" {
			value = "no routes"
			if !node.Ready && node.Status != "stopped" {
				value = "none until ready"
			}
		}
		result = append(result, plainLogText(node.Slug)+"  "+value)
	}
	return result
}
