//go:build cgo && (darwin || linux)

package codelima

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"git.sr.ht/~rockorager/vaxis"
)

const (
	rendererWorkerFD             = 3
	rendererPublishMinInterval   = 50 * time.Millisecond
	rendererSlowCallLogThreshold = 250 * time.Millisecond
)

type rendererWorkerServer struct {
	ctx        context.Context
	conn       net.Conn
	writeMu    sync.Mutex
	publishMu  sync.Mutex
	publisher  *coalescedPublisher
	generation uint64
	terminalID string
	terminal   *ghosttyTUITerminal
	replaying  atomic.Bool
}

// RunRendererWorker serves one Ghostty renderer over the inherited descriptor
// passed by the daemon-side terminal supervisor.
func RunRendererWorker(ctx context.Context) error {
	file := os.NewFile(rendererWorkerFD, "codelima-renderer-control")
	if file == nil {
		return errors.New("renderer control descriptor is unavailable")
	}
	conn, err := net.FileConn(file)
	_ = file.Close()
	if err != nil {
		return fmt.Errorf("open renderer control connection: %w", err)
	}
	defer func() { _ = conn.Close() }()
	return (&rendererWorkerServer{ctx: ctx, conn: conn}).run()
}

func (s *rendererWorkerServer) run() error {
	go func() {
		<-s.ctx.Done()
		_ = s.conn.Close()
	}()
	defer func() {
		if s.terminal != nil {
			s.terminal.Close()
		}
	}()
	publishCtx, cancelPublish := context.WithCancel(s.ctx)
	publishDone := make(chan struct{})
	s.publisher = newCoalescedPublisher(rendererPublishMinInterval, s.publish)
	go func() {
		s.publisher.Run(publishCtx)
		close(publishDone)
	}()
	defer func() {
		cancelPublish()
		<-publishDone
	}()

	for {
		frame, err := readRendererFrame(s.conn)
		if err != nil {
			if s.ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		if frame.Type != rendererFrameRequest {
			return fmt.Errorf("renderer received unexpected frame type %q", frame.Type)
		}
		if s.generation != 0 && frame.Generation != s.generation {
			continue
		}
		started := time.Now()
		result, requestErr := s.handle(frame)
		elapsed := time.Since(started)
		if requestErr != nil {
			slog.Error(
				"renderer call failed",
				"terminal_id", s.terminalID,
				"renderer_generation", frame.Generation,
				"operation", frame.Method,
				"request_id", frame.ID,
				"elapsed", elapsed,
				"error", requestErr,
			)
		} else if elapsed >= rendererSlowCallLogThreshold {
			slog.Warn(
				"renderer call slow",
				"terminal_id", s.terminalID,
				"renderer_generation", frame.Generation,
				"operation", frame.Method,
				"request_id", frame.ID,
				"elapsed", elapsed,
			)
		}
		if frame.ID == 0 || frame.NoReply {
			continue
		}
		response := rendererWorkerFrame{
			Type:       rendererFrameResponse,
			ID:         frame.ID,
			Generation: frame.Generation,
		}
		if requestErr != nil {
			response.Error = requestErr.Error()
		} else if result != nil {
			response.Result, requestErr = json.Marshal(result)
			if requestErr != nil {
				response.Error = requestErr.Error()
			}
		}
		if err := s.write(response); err != nil {
			return err
		}
		if frame.Method == "close" {
			return nil
		}
	}
}

func (s *rendererWorkerServer) handle(frame rendererWorkerFrame) (any, error) {
	switch frame.Method {
	case "init":
		if s.terminal != nil {
			return nil, errors.New("renderer is already initialized")
		}
		var params rendererInitParams
		if err := json.Unmarshal(frame.Params, &params); err != nil {
			return nil, err
		}
		if params.TerminalID == "" || params.Cols <= 0 || params.Rows <= 0 {
			return nil, errors.New("renderer init requires terminal id and positive geometry")
		}
		base, err := newGhosttyTUITerminal(params.TerminalID, s.postEvent)
		if err != nil {
			return nil, err
		}
		terminal, ok := base.(*ghosttyTUITerminal)
		if !ok {
			base.Close()
			return nil, errors.New("renderer worker requires the Ghostty backend")
		}
		s.generation = frame.Generation
		s.terminalID = params.TerminalID
		s.terminal = terminal
		terminal.mu.Lock()
		terminal.rendererOutput = s.postPTYWrite
		terminal.mu.Unlock()
		terminal.Resize(params.Cols, params.Rows)
		s.replaying.Store(true)
		for _, event := range params.Journal {
			s.applyJournalEvent(event)
		}
		s.replaying.Store(false)
		s.publish()
		return map[string]bool{"ready": true}, nil
	case "output":
		var params rendererOutputParams
		if err := json.Unmarshal(frame.Params, &params); err != nil {
			return nil, err
		}
		s.terminal.sendSync(cmdRendererOutput(params))
		return map[string]bool{"applied": true}, nil
	case "resize":
		var params rendererResizeParams
		if err := json.Unmarshal(frame.Params, &params); err != nil {
			return nil, err
		}
		s.terminal.Resize(params.Cols, params.Rows)
		return map[string]bool{"applied": true}, nil
	case "update":
		var params rendererUpdateParams
		if err := json.Unmarshal(frame.Params, &params); err != nil {
			return nil, err
		}
		event, err := decodeRendererInputEvent(params.Event)
		if err != nil {
			return nil, err
		}
		s.terminal.sendSync(cmdRendererUpdate{EventID: rendererInputEventID(frame.ID), Event: event})
		return map[string]bool{"applied": true}, nil
	case "focus":
		var params rendererInputEvent
		if err := json.Unmarshal(frame.Params, &params); err != nil {
			return nil, err
		}
		if params.Focused {
			s.terminal.Focus()
		} else {
			s.terminal.Blur()
		}
		return map[string]bool{"applied": true}, nil
	case "scroll":
		var params rendererInputEvent
		if err := json.Unmarshal(frame.Params, &params); err != nil {
			return nil, err
		}
		s.terminal.Scroll(params.Delta)
		return map[string]bool{"applied": true}, nil
	case "snapshot":
		s.publish()
		return map[string]bool{"published": true}, nil
	case "health":
		if s.terminal == nil {
			return nil, errors.New("renderer is not initialized")
		}
		if !s.terminal.rendererHealth() {
			return nil, errors.New("renderer actor stopped")
		}
		return map[string]bool{"healthy": true}, nil
	case "close":
		s.publishMu.Lock()
		s.terminal.Close()
		s.terminal = nil
		s.publishMu.Unlock()
		return map[string]bool{"closed": true}, nil
	default:
		return nil, fmt.Errorf("unknown renderer method %q", frame.Method)
	}
}

func (s *rendererWorkerServer) applyJournalEvent(event rendererJournalEvent) {
	switch event.Type {
	case "output":
		s.terminal.sendSync(cmdRendererOutput{EventID: event.ID, Data: event.Data})
	case "resize":
		s.terminal.Resize(event.Cols, event.Rows)
	}
}

func (s *rendererWorkerServer) requestPublish() {
	if s.publisher == nil {
		s.publish()
		return
	}
	s.publisher.Request()
}

func (s *rendererWorkerServer) publish() {
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	if s.terminal == nil {
		return
	}
	snapshot := s.terminal.Snapshot()
	if snapshot.Err != nil {
		return
	}
	state := rendererPublishedState{
		Snapshot:    snapshot.Snapshot,
		VisibleText: readResultDTO(s.terminal.ReadVisible(ReadText)),
		VisibleANSI: readResultDTO(s.terminal.ReadVisible(ReadANSI)),
		RecentText:  readResultDTO(s.terminal.ReadRecent(ReadText)),
		RecentANSI:  readResultDTO(s.terminal.ReadRecent(ReadANSI)),
	}
	result, err := json.Marshal(state)
	if err != nil {
		return
	}
	_ = s.write(rendererWorkerFrame{
		Type:       rendererFrameSnapshot,
		Generation: s.generation,
		Result:     result,
	})
}

func (s *rendererWorkerServer) postPTYWrite(eventID uint64, ordinal uint32, data []byte) {
	// Replaying emulator input may reproduce terminal-query responses whose
	// original delivery outcome is unknown. Suppress them during replay;
	// emitting a duplicate response to the live application is less safe than
	// omitting one uncertain response while the screen is reconstructed.
	if s.replaying.Load() {
		return
	}
	result, err := json.Marshal(data)
	if err != nil {
		return
	}
	_ = s.write(rendererWorkerFrame{
		Type:       rendererFramePTYWrite,
		Generation: s.generation,
		EventID:    eventID,
		Ordinal:    ordinal,
		Result:     result,
	})
}

func (s *rendererWorkerServer) postEvent(event vaxis.Event) {
	frame := rendererWorkerFrame{Type: rendererFrameEvent, Generation: s.generation}
	switch value := event.(type) {
	case vaxis.Redraw:
		s.requestPublish()
		return
	case tuiClipboardEvent:
		frame.Event = "clipboard"
		frame.Result, _ = json.Marshal(value.Text)
	case tuiTerminalErrorEvent:
		frame.Event = "error"
		frame.Error = value.Err.Error()
	case tuiTerminalClosedEvent:
		frame.Event = "closed"
		if value.Err != nil {
			frame.Error = value.Err.Error()
		}
	default:
		return
	}
	_ = s.write(frame)
}

func (s *rendererWorkerServer) write(frame rendererWorkerFrame) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return writeRendererFrame(s.conn, frame)
}

func decodeRendererInputEvent(event rendererInputEvent) (vaxis.Event, error) {
	switch event.Type {
	case "key":
		return vaxis.Key{
			Keycode:     event.Keycode,
			ShiftedCode: event.Shifted,
			Text:        event.Text,
			Modifiers:   vaxis.ModifierMask(event.Modifiers),
			EventType:   vaxis.EventType(event.EventType),
		}, nil
	case "mouse":
		return vaxis.Mouse{
			Col:       event.Col,
			Row:       event.Row,
			Button:    vaxis.MouseButton(event.Button),
			EventType: vaxis.EventType(event.EventType),
		}, nil
	case "paste_start":
		return vaxis.PasteStartEvent{}, nil
	case "paste_end":
		return vaxis.PasteEndEvent{}, nil
	case "focus":
		if event.Focused {
			return vaxis.FocusIn{}, nil
		}
		return vaxis.FocusOut{}, nil
	default:
		return nil, fmt.Errorf("unsupported renderer input event %q", event.Type)
	}
}
