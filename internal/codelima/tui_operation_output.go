package codelima

import (
	"context"
	"fmt"
	"slices"
	"time"

	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/border"
)

// Receipt output belongs to the operation, so it remains readable after its
// disposable node has been deleted. Closing this view only cancels its query.
type tuiOperationOutputView struct {
	app              *vaxisTUIApp
	id               string
	record           NodeOperation
	err              error
	inFlight         bool
	cancel           context.CancelFunc
	lastLoad         time.Time
	scroll, viewport int
	follow           bool
}
type tuiOperationOutputEvent struct {
	view   *tuiOperationOutputView
	record NodeOperation
	err    error
}

func (a *vaxisTUIApp) openOperationOutput(id string, record NodeOperation) {
	v := &tuiOperationOutputView{app: a, id: id, record: record, follow: true}
	a.overlay = v
	v.load()
}

func (v *tuiOperationOutputView) load() {
	if v.inFlight || time.Since(v.lastLoad) < time.Second || v.app.service == nil || v.app.service.daemonClient == nil {
		return
	}
	ctx := v.app.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	v.cancel, v.inFlight, v.lastLoad = cancel, true, time.Now()
	client, id := v.app.service.daemonClient, v.id
	load := func() tuiOperationOutputEvent {
		defer cancel()
		e := tuiOperationOutputEvent{view: v}
		e.err = fromDaemonError(client.Call(ctx, "operation.get", map[string]string{"id": id}, &e.record))
		return e
	}
	if v.app.postEvent == nil {
		v.app.applyOperationOutput(load())
		return
	}
	go func() { v.app.postCompletion(load()) }()
}

func (a *vaxisTUIApp) applyOperationOutput(e tuiOperationOutputEvent) {
	v := e.view
	if a.overlay != v {
		return
	}
	v.inFlight, v.err = false, e.err
	if e.err == nil && e.record.ID == v.id {
		v.record = e.record
	}
}

func (v *tuiOperationOutputView) Update(event vaxis.Event) (bool, error) {
	if mouse, ok := event.(vaxis.Mouse); ok {
		if mouse.Button == vaxis.MouseWheelUp {
			v.follow = false
			v.scroll = max(0, v.scroll-3)
		}
		if mouse.Button == vaxis.MouseWheelDown {
			v.follow = false
			v.scroll += 3
		}
		return false, nil
	}
	key, ok := event.(vaxis.Key)
	if !ok || !tuiKeyActivates(key, false) {
		return false, nil
	}
	if isOverlayCancelKey(key) && tuiKeyActivates(key, true) {
		if v.cancel != nil {
			v.cancel()
		}
		return true, nil
	}
	switch {
	case key.MatchString("K") && tuiKeyActivates(key, true):
		if v.record.State == "running" || v.record.State == "canceling" {
			v.app.requestOperationCancel(v.id)
		}
	case key.Matches(vaxis.KeyUp), key.MatchString("k"):
		v.follow = false
		v.scroll--
	case key.Matches(vaxis.KeyDown), key.MatchString("j"):
		v.follow = false
		v.scroll++
	case key.Matches(vaxis.KeyPgUp):
		v.follow = false
		v.scroll -= max(1, v.viewport-1)
	case key.Matches(vaxis.KeyPgDown):
		v.follow = false
		v.scroll += max(1, v.viewport-1)
	case key.Matches(vaxis.KeyHome), key.MatchString("g"):
		v.follow, v.scroll = false, 0
	case key.Matches(vaxis.KeyEnd), key.MatchString("G"):
		v.follow = true
	}
	v.scroll = max(0, v.scroll)
	return false, nil
}

func (v *tuiOperationOutputView) FooterHint() string {
	hint := "↑↓ scroll · PgUp/PgDn page · G follow"
	if v.record.State == "running" || v.record.State == "canceling" {
		hint += " · K cancel"
	}
	return hint + " · Esc close"
}

func (v *tuiOperationOutputView) Draw(win vaxis.Window, header, muted, errorStyle vaxis.Style) {
	body := border.All(win, muted)
	w, h := body.Size()
	r := v.record
	title := coalesce(r.Request.Kind, "Operation output")
	if r.EnvironmentTest != nil {
		title = "Test " + r.EnvironmentTest.EnvironmentSlug
	}
	tuiLine(body, 0, plainLogText(title+" · "+coalesce(r.State, "loading")), header)
	detail := r.Error
	if r.EnvironmentTest != nil {
		if r.EnvironmentTest.CleanupPending {
			detail = "Disposable VM · cleanup pending"
		} else {
			detail = "Disposable VM and workspace deleted"
		}
		if r.EnvironmentTest.CleanupError != "" {
			detail += " · retrying: " + r.EnvironmentTest.CleanupError
		}
		if r.Error != "" {
			detail += " · " + r.Error
		}
	}
	tuiLine(body, 1, plainLogText(detail), muted)
	if v.err != nil {
		tuiLine(body, 1, "Refresh failed: "+plainLogText(v.err.Error())+" · retrying", errorStyle)
	}
	phase := r.CurrentPhase
	for _, p := range r.Phases {
		if p.ID == r.CurrentPhase {
			phase = p.Label + " · " + p.Detail
		}
	}
	tuiLine(body, 2, plainLogText(phase), muted)
	lines := slices.Clone(r.Output)
	if r.OutputPartial != "" {
		lines = append(lines, r.OutputPartial)
	}
	var wrapped []string
	for _, line := range lines {
		for _, visual := range commandVisualRows(vaxis.Characters(plainLogText(line)), max(1, w)) {
			wrapped = append(wrapped, visual.text)
		}
	}
	v.viewport = max(1, h-6)
	if v.follow {
		v.scroll = len(wrapped) - v.viewport
	}
	v.scroll = max(0, min(v.scroll, max(0, len(wrapped)-v.viewport)))
	for i, line := range wrapped[v.scroll:min(len(wrapped), v.scroll+v.viewport)] {
		tuiLine(body, i+4, line, muted)
	}
	if len(wrapped) == 0 {
		tuiLine(body, 4, "Awaiting output…", muted)
	}
	mode := "paused"
	if v.follow {
		mode = "following"
	}
	tuiLine(body, h-2, fmt.Sprintf("Output · %s · retained from line %d · %s", mode, r.OutputStart+1, v.id), muted)
	tuiLine(body, h-1, v.FooterHint(), muted)
}
