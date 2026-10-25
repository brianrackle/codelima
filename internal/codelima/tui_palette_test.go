package codelima

import (
	"context"
	"slices"
	"strings"
	"testing"

	"go.rockorager.dev/vaxis"
)

func paletteQuery(p *tuiPalette, query string) {
	p.input.SetContent(query)
	p.filter()
}

func TestTUIManagerPaletteGroupsAndBracketedPaste(t *testing.T) {
	a, _ := newTestManager(t, 150, 38)
	a.openCommandPalette()
	p := a.overlay.(*tuiPalette)
	a.draw()
	screen := renderedScreenText(t, a.vx, 150, 38)
	for _, text := range []string{"NODES", "ACTIONS", "results", ": "} {
		if !strings.Contains(screen, text) {
			t.Fatalf("missing %q:\n%s", text, screen)
		}
	}
	for _, r := range "status: failed" {
		a.handleKey(vaxis.Key{Keycode: r, Text: string(r), EventType: vaxis.EventPaste})
	}
	_, _ = a.handleEvent(vaxis.PasteEndEvent{})
	if len(p.matches) != 1 || p.matches[0].id != "filter:failed" {
		t.Fatalf("paste matches = %+v", p.matches)
	}
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyEnter, EventType: vaxis.EventRelease})
	if a.fleet.filter != "" {
		t.Fatal("release ran filter")
	}
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyEnter})
	if a.fleet.filter != "status:failed" || a.view != tuiViewNodes {
		t.Fatal("filter command did not run")
	}
	a.openCommandPalette()
	p = a.overlay.(*tuiPalette)
	if len(p.matches) != 1 || p.matches[0].id != "filter:failed" || p.matches[0].group != "RECENT" {
		t.Fatalf("empty query lost recent command: %+v", p.matches)
	}
}

func TestTUIManagerPaletteNodeIdentityIgnoresSelectionAndMarks(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	a.service, _ = newTestService(t)
	cfg := a.service.cfg
	cfg.ConfirmStop = "always"
	a.preferences = &cfg
	wanted, other := a.state.nodes[0], a.state.nodes[1]
	a.openCommandPalette()
	p := a.overlay.(*tuiPalette)
	paletteQuery(p, "restart "+wanted.Slug)
	if len(p.matches) != 1 {
		t.Fatalf("restart matches=%+v", p.matches)
	}
	a.fleet.selectID(other.ID)
	a.selectFleetNode()
	a.fleet.marks[other.ID] = true
	if _, err := p.choose(); err != nil {
		t.Fatal(err)
	}
	d := a.activeDialog()
	if d == nil || d.Title != "Restart "+wanted.Slug {
		t.Fatalf("wrong confirmation: %+v", d)
	}
	if !a.fleet.marks[other.ID] {
		t.Fatal("explicit node command cleared unrelated mark")
	}
	// A disappearing target must be refused at confirmation, before any RPC.
	a.state.nodes = slices.DeleteFunc(a.state.nodes, func(n Node) bool { return n.ID == wanted.ID })
	if err := d.OnSubmit(nil); err == nil || !strings.Contains(err.Error(), "no longer") {
		t.Fatalf("removed target submitted: %v", err)
	}
}

func TestTUIManagerPaletteRefreshRetainsUnavailableSelection(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	a.openCommandPalette()
	p := a.overlay.(*tuiPalette)
	wanted := a.state.nodes[0]
	paletteQuery(p, "restart "+wanted.Slug)
	id := p.matches[0].id
	a.state.nodes[0].Status = NodeStatusStopped
	if err := a.state.replaceNodes(a.state.nodes, ""); err != nil {
		t.Fatal(err)
	}
	a.draw()
	if len(p.matches) != 1 || p.matches[p.index].id != id || !p.matches[p.index].unavailable {
		t.Fatal("refresh silently selected another command", p.matches)
	}
	if _, err := p.choose(); err == nil {
		t.Fatal("unavailable command executed")
	}
	paletteQuery(p, "start "+wanted.Slug)
	if len(p.matches) == 0 || p.matches[0].unavailable || p.matches[0].id != paletteNodeCommandID(wanted.ID, tuiActionNodeStart) {
		t.Fatal("explicit new query did not select current action", p.matches)
	}
}

func TestTUIManagerPaletteFuzzyPositionsUseRunes(t *testing.T) {
	score, positions := paletteFuzzyMatch("stop test-hardening", "sta")
	if score < 0 || !slices.Equal(positions, []int{0, 1, 11}) {
		t.Fatalf("fuzzy match=%d %v", score, positions)
	}
	_, positions = paletteFuzzyMatch("réstart 界", "ÉST")
	if !slices.Equal(positions, []int{1, 2, 3}) {
		t.Fatal("Unicode positions", positions)
	}
}

func TestTUIManagerPaletteDirectoryBatchCapturesMembersAndRetriesSuffix(t *testing.T) {
	s, first := setupBootstrapStepsTest(t)
	ctx := context.Background()
	fake := s.sandbox.(*fakeSandbox)
	fake.failCommand = "step-second"
	if _, err := s.NodeStart(ctx, first.ID); err == nil {
		t.Fatal("expected failed setup")
	}
	fake.failCommand = ""
	var err error
	first, err = s.NodeShow(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.NodeCreate(ctx, NodeCreateInput{Slug: "palette-second", Directory: first.DirectoryPath})
	if err != nil {
		t.Fatal(err)
	}
	a, sessions := newTestManager(t, 80, 24)
	a.service = batchTestFrontend(t, s)
	a.state, err = newTUIState([]Node{first, second}, sessions)
	if err != nil {
		t.Fatal(err)
	}
	a.refreshFleet()
	a.fleet.marks[second.ID] = true
	a.openCommandPalette()
	p := a.overlay.(*tuiPalette)
	paletteQuery(p, "start all in")
	if len(p.matches) == 0 || !strings.HasPrefix(p.matches[0].id, "start-directory:") || !strings.Contains(p.matches[0].hint, "retry") {
		t.Fatalf("batch omitted failed retry: %+v", p.matches)
	}
	if _, err := p.choose(); err != nil {
		t.Fatal(err)
	}
	d := a.activeDialog()
	if d == nil {
		t.Fatal("start all did not review targets")
	}
	third, err := s.NodeCreate(ctx, NodeCreateInput{Slug: "palette-later", Directory: first.DirectoryPath})
	if err != nil {
		t.Fatal(err)
	}
	a.state.nodes = append(a.state.nodes, third)
	if err := d.OnSubmit(nil); err != nil {
		t.Fatal(err)
	}
	bootstrap, err := s.store.LoadBootstrapState(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bootstrap.Completed || bootstrap.Steps[0].Attempts != 1 || bootstrap.Steps[1].Attempts != 2 {
		t.Fatal("directory start restarted completed bootstrap", bootstrap.Steps)
	}
	started, err := s.NodeShow(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	untouched, err := s.NodeShow(ctx, third.ID)
	if err != nil {
		t.Fatal(err)
	}
	if started.Status != NodeStatusRunning || nodeIsRunning(untouched) {
		t.Fatal("batch skipped or enlarged captured membership", started.Status, untouched.Status)
	}
	if !a.fleet.marks[second.ID] {
		t.Fatal("directory action cleared unrelated marks")
	}
}

func TestTUIManagerPaletteBatchRejectsChangedMemberBeforeFirstAction(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	for i := range a.state.nodes {
		a.state.nodes[i].Status = NodeStatusStopped
	}
	a.refreshFleet()
	plan := []tuiPaletteNodePlan{{a.state.nodes[0].ID, tuiActionNodeStart}, {a.state.nodes[1].ID, tuiActionNodeStart}}
	if err := a.reviewPaletteBatch("Start directory", plan); err != nil {
		t.Fatal(err)
	}
	d := a.activeDialog()
	a.state.nodes[1].Status = NodeStatusRunning
	if err := d.OnSubmit(nil); err == nil {
		t.Fatal("changed second member admitted first action")
	}
}

func TestTUIManagerPaletteCompletionAndActivationDoNotRepeat(t *testing.T) {
	runs := 0
	p := newTUIPalette("Commands", []tuiPaletteCommand{{label: "Create node", run: func() error { runs++; return nil }}})
	paletteQuery(p, "crnd")
	_, _ = p.Update(vaxis.Key{Keycode: vaxis.KeyTab})
	for _, key := range []vaxis.Key{{Keycode: vaxis.KeyTab, Text: "\t", EventType: vaxis.EventRepeat}, {Keycode: vaxis.KeyEnter, Text: "\r", EventType: vaxis.EventRepeat}} {
		_, _ = p.Update(key)
	}
	if p.input.String() != "Create node" || runs != 0 {
		t.Fatal("held shortcut edited or executed", p.input.String(), runs)
	}
}

func TestTUIManagerPaletteMouseIdentityAndCompactHints(t *testing.T) {
	for _, size := range [][2]int{{150, 38}, {80, 24}} {
		a, _ := newTestManager(t, size[0], size[1])
		a.openCommandPalette()
		p := a.overlay.(*tuiPalette)
		paletteQuery(p, "Node: "+a.state.nodes[1].Slug)
		a.draw()
		if !strings.Contains(renderedScreenText(t, a.vx, size[0], size[1]), "/work/project") {
			t.Fatal("node context disappeared")
		}
		var target tuiCommandTarget
		for _, row := range p.rows {
			if row.id == "node:"+a.state.nodes[1].ID {
				target = row
				break
			}
		}
		if target.id == "" {
			t.Fatal("node command has no mouse region")
		}
		id := a.state.nodes[1].ID
		a.state.nodes = slices.DeleteFunc(a.state.nodes, func(n Node) bool { return n.ID == id })
		done, err := p.Update(vaxis.Mouse{Col: target.rect.col, Row: target.rect.row, Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress})
		if done || err == nil || a.nodeDetail {
			t.Fatal("stale mouse target navigated to replacement", done, err)
		}
	}
}

func TestTUIManagerPaletteHighlightsMatchesAndKeepsSelectionOnResize(t *testing.T) {
	a, _ := newTestManager(t, 150, 38)
	a.openCommandPalette()
	p := a.overlay.(*tuiPalette)
	p.refresh = func() []tuiPaletteCommand {
		return []tuiPaletteCommand{{id: "stop", group: "NODES", label: "stop test-hardening", hint: "running · 2 guest sessions end", shortcut: "s"}}
	}
	paletteQuery(p, "sta")
	if _, height := p.PreferredSize(); height > 10 {
		t.Fatal("one-result palette reserves an empty full-height modal", height)
	}
	for _, size := range []vaxis.Resize{{Cols: 150, Rows: 38}, {Cols: 80, Rows: 24}, {Cols: 40, Rows: 10}, {Cols: 150, Rows: 38}} {
		if _, err := a.handleEvent(size); err != nil {
			t.Fatal(err)
		}
		a.draw()
		if p.matches[p.index].id != "stop" || p.input.String() != "sta" {
			t.Fatal("resize changed selected command or query")
		}
		if size.Cols < 60 {
			continue
		}
		if len(p.rows) == 0 {
			t.Fatal("selected row is not visible")
		}
		row := p.rows[0].rect
		for col, char := range "stop test-hardening" {
			style := renderedCellStyle(t, a.vx, row.col+2+col, row.row)
			matched := col == 0 || col == 1 || col == 11
			if (style.UnderlineStyle == vaxis.UnderlineSingle) != matched {
				t.Fatalf("column %d (%c): incorrect match style %+v", col, char, style)
			}
		}
		for _, target := range p.rows {
			if target.rect.col+target.rect.width > size.Cols || target.rect.row >= size.Rows {
				t.Fatal("mouse target outside resized terminal", target)
			}
		}
	}
	for _, control := range p.controls {
		if control.action == "palette.complete" {
			a.handleMouse(vaxis.Mouse{Col: control.rect.col, Row: control.rect.row, Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress})
			if p.input.String() != "stop test-hardening" {
				t.Fatal("mouse completion did not preserve command")
			}
			return
		}
	}
	t.Fatal("missing completion control")
}

func TestTUIManagerPaletteUnavailableRecentFallsBackToCurrentCommands(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	id := a.state.nodes[0].ID
	a.paletteRecent = []string{"node:" + id}
	a.openCommandPalette()
	p := a.overlay.(*tuiPalette)
	if len(p.matches) != 1 || p.matches[0].id != "node:"+id {
		t.Fatal("recent navigation unavailable")
	}
	a.state.nodes = slices.DeleteFunc(a.state.nodes, func(n Node) bool { return n.ID == id })
	a.openCommandPalette()
	p = a.overlay.(*tuiPalette)
	a.draw()
	if len(p.matches) == 0 || slices.ContainsFunc(p.matches, func(c tuiPaletteCommand) bool { return c.group == "RECENT" || c.id == "node:"+id }) {
		t.Fatal("stale history hid available commands", p.matches)
	}
}

func TestTUIManagerPaletteMemoryWarningRequiresCompleteObservation(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	node := a.state.nodes[0]
	node.MemoryMiB = 4096
	total := uint64(8 << 30)
	a.host.Capacity.MemoryBytes = &total
	a.host.AllocatedMemoryBytes = 6 << 30
	if _, warning := a.paletteNodeHint(node, tuiActionNodeStart); warning {
		t.Fatal("unknown allocations invented a warning")
	}
	a.host.AllocationsKnown = true
	hint, warning := a.paletteNodeHint(node, tuiActionNodeStart)
	if !warning || !strings.Contains(hint, "unallocated (cached)") {
		t.Fatal("known shortfall missing cached allocation context", hint)
	}
	a.host.UnknownNodes = 1
	if _, warning := a.paletteNodeHint(node, tuiActionNodeStart); warning {
		t.Fatal("partial allocations claimed known memory shortfall")
	}
	a.daemonDisconnected = true
	hint, _ = a.paletteNodeHint(node, tuiActionNodeStop)
	if !strings.Contains(hint, "impact unavailable") || strings.Contains(hint, "0 known") {
		t.Fatal("lost daemon claimed no affected sessions", hint)
	}
}
