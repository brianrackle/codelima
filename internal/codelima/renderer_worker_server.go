//go:build darwin || linux

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

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/terminalgraphics"
	"github.com/brianrackle/codelima/internal/terminalstate"
	"go.rockorager.dev/vaxis"
)

const (
	rendererWorkerFD             = 3
	rendererPublishMinInterval   = 50 * time.Millisecond
	rendererSlowCallLogThreshold = 250 * time.Millisecond
)

type rendererWorkerServer struct {
	ctx                   context.Context
	conn                  net.Conn
	writeMu               sync.Mutex
	publishMu             sync.Mutex
	publisher             *coalescedPublisher
	generation            uint64
	terminalID            string
	terminal              RendererTerminal
	factory               RendererTerminalFactory
	replaying             atomic.Bool
	journalOrder          rendererJournalOrder
	journalGap            atomic.Bool
	gapTimeout            time.Duration
	partialRecovery       bool
	graphics              rendererGraphicsAssets
	lastGraphicsError     string
	cellWidth, cellHeight int
}

// RunRendererWorker serves one Ghostty renderer over the inherited descriptor
// passed by the daemon-side terminal supervisor.
func RunRendererWorker(ctx context.Context, factory RendererTerminalFactory) error {
	if factory == nil {
		return errors.New("renderer native terminal factory is unavailable")
	}
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
	return (&rendererWorkerServer{ctx: ctx, conn: conn, factory: factory}).run()
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

	var deferred []rendererWorkerFrame
	deferredBytes := 0
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
		if s.journalOrder.hasGap() && frame.Method != "output" && frame.Method != "resize" && frame.Method != "health" && frame.Method != "close" && frame.Method != "checkpoint" {
			if len(deferred) >= defaultRendererQueueFrames || len(frame.Params) > defaultRendererJournalBytes-deferredBytes {
				return errors.New("renderer deferred command buffer is full while waiting for journal order")
			}
			deferred = append(deferred, frame)
			deferredBytes += len(frame.Params)
			continue
		}
		if err := s.serveFrame(frame); err != nil {
			return err
		}
		if frame.Method == "close" {
			return nil
		}
		if !s.journalOrder.hasGap() {
			for _, waiting := range deferred {
				if err := s.serveFrame(waiting); err != nil {
					return err
				}
			}
			deferred = nil
			deferredBytes = 0
		}
	}
}

func (s *rendererWorkerServer) serveFrame(frame rendererWorkerFrame) error {
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
		// Journal rejection cannot silently remove an event from the applied
		// prefix. Exit so the supervisor can replay its durable copy.
		return requestErr
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
	return nil
}

func (s *rendererWorkerServer) handle(frame rendererWorkerFrame) (any, error) {
	if frame.Method != "init" && s.terminal == nil {
		return nil, errors.New("renderer is not initialized")
	}
	switch frame.Method {
	case "init":
		if s.terminal != nil {
			return nil, errors.New("renderer is already initialized")
		}
		var params rendererInitParams
		if err := json.Unmarshal(frame.Params, &params); err != nil {
			return nil, err
		}
		if params.TerminalID == "" || !rendererValidGeometry(params.Cols, params.Rows) || !rendererValidPixels(params.CellWidth, params.CellHeight) {
			return nil, errors.New("renderer init requires terminal id and positive geometry")
		}
		if err := terminalstate.ValidateColors(params.Colors); err != nil {
			return nil, err
		}
		terminal, err := s.factory(params.TerminalID, s.postEvent, s.postPTYWrite)
		if err != nil {
			return nil, err
		}
		s.generation = frame.Generation
		s.terminalID = params.TerminalID
		s.terminal = terminal
		s.gapTimeout = time.Duration(params.CommandTimeoutMillis) * time.Millisecond
		if s.gapTimeout <= 0 || s.gapTimeout > maxRendererInitDeadline {
			s.gapTimeout = defaultRendererCommandTimeout
		}
		s.partialRecovery = params.JournalPartial
		s.replaying.Store(true)
		defer s.replaying.Store(false)
		terminal.Resize(params.Cols, params.Rows)
		if params.CellWidth > 0 {
			if err := terminal.ResizePixels(params.Cols, params.Rows, params.CellWidth, params.CellHeight); err != nil {
				return nil, err
			}
			s.cellWidth, s.cellHeight = params.CellWidth, params.CellHeight
		}
		restored := false
		if checkpoint := params.Checkpoint; checkpoint != nil && checkpoint.validate(rendererNativeBuildIdentity(), params.TerminalID) == nil {
			tail := rendererJournalSnapshot{Events: params.CheckpointTail, LastID: params.JournalWatermark}
			if _, complete := tail.tailAfter(checkpoint.AppliedEventID); complete {
				state := checkpoint.State
				if params.Colors != nil {
					state.Colors = terminalstate.CloneColors(params.Colors)
				}
				if err := terminal.RestoreCheckpoint(state); err == nil {
					s.cellWidth, s.cellHeight = checkpoint.State.CellWidth, checkpoint.State.CellHeight
					restored = true
					s.partialRecovery = checkpoint.Partial
					for _, event := range params.CheckpointTail {
						s.applyJournalEvent(event)
					}
				} else {
					// A failed native decode may have mutated its target. Raw replay
					// always starts on a fresh native terminal, never that target.
					terminal.Close()
					terminal, err = s.factory(params.TerminalID, s.postEvent, s.postPTYWrite)
					if err != nil {
						return nil, err
					}
					s.terminal = terminal
					terminal.Resize(params.Cols, params.Rows)
					if params.CellWidth > 0 {
						if err := terminal.ResizePixels(params.Cols, params.Rows, params.CellWidth, params.CellHeight); err != nil {
							return nil, err
						}
					}
				}
			}
		}
		if !restored {
			if params.Colors != nil {
				if _, err := terminal.Interact(TerminalInteractionRequest{Action: "colors", Colors: params.Colors}); err != nil {
					return nil, err
				}
			}
			for _, event := range params.Journal {
				s.applyJournalEvent(event)
			}
		}
		s.journalOrder.applied = params.JournalWatermark
		s.replaying.Store(false)
		// A protocol match alone is insufficient readiness: replay may have
		// returned a fatal native error without stopping the actor. Validate one
		// coherent publication before acknowledging initialization. The daemon
		// requests its first installable frame after verifying this reply.
		snapshot, visible := terminal.Publish()
		if snapshot.Err != nil {
			return nil, snapshot.Err
		}
		if visible.Err != nil {
			return nil, visible.Err
		}
		// The version rides the init reply, so the supervisor learns it before
		// it installs the link and before this worker's first snapshot frame is
		// interpreted.
		return rendererInitResult{Protocol: rendererWorkerProtocolVersion, Ready: true, BuildID: rendererNativeBuildIdentity(), RestoredCheckpoint: restored, PartialRecovery: s.partialRecovery}, nil
	case "output":
		var params rendererOutputParams
		if err := json.Unmarshal(frame.Params, &params); err != nil {
			return nil, err
		}
		err := s.journalOrder.push(rendererJournalEvent{ID: params.EventID, Type: "output", Data: params.Data}, s.applyJournalEvent)
		s.journalGap.Store(s.journalOrder.hasGap())
		return map[string]bool{"applied": err == nil}, err
	case "resize":
		var params rendererResizeParams
		if err := json.Unmarshal(frame.Params, &params); err != nil {
			return nil, err
		}
		err := s.journalOrder.push(rendererJournalEvent{ID: params.EventID, FirstID: params.FirstID, Type: "resize", Cols: params.Cols, Rows: params.Rows, CellWidth: params.CellWidth, CellHeight: params.CellHeight}, s.applyJournalEvent)
		s.journalGap.Store(s.journalOrder.hasGap())
		return map[string]bool{"applied": err == nil}, err
	case "checkpoint":
		var params rendererCheckpointParams
		if err := json.Unmarshal(frame.Params, &params); err != nil {
			return nil, err
		}
		if s.journalOrder.hasGap() || s.journalOrder.applied < params.Through {
			return nil, errors.New("renderer checkpoint is waiting for journal watermark")
		}
		state, err := s.terminal.Checkpoint()
		if err != nil {
			return nil, err
		}
		state.CellWidth, state.CellHeight = s.cellWidth, s.cellHeight
		return newRendererCheckpoint(rendererNativeBuildIdentity(), s.terminalID, s.generation, s.journalOrder.applied, s.partialRecovery, state)
	case "update":
		var params rendererUpdateParams
		if err := json.Unmarshal(frame.Params, &params); err != nil {
			return nil, err
		}
		event, err := decodeRendererInputEvent(params.Event)
		if err != nil {
			return nil, err
		}
		s.terminal.UpdateEvent(rendererInputEventID(frame.ID), event)
		return map[string]bool{"applied": true}, nil
	case "paste":
		var params rendererPasteParams
		if err := json.Unmarshal(frame.Params, &params); err != nil {
			return nil, err
		}
		return s.terminal.EncodePaste(params.Text)
	case "interact":
		var params TerminalInteractionRequest
		if err := json.Unmarshal(frame.Params, &params); err != nil {
			return nil, err
		}
		return s.terminal.Interact(params)
	case "graphics":
		var params daemon.TerminalGraphicsParams
		if err := json.Unmarshal(frame.Params, &params); err != nil {
			return nil, err
		}
		return s.graphics.read(params)
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
	case "read":
		if s.terminal == nil {
			return nil, errors.New("renderer is not initialized")
		}
		var params rendererReadParams
		if err := json.Unmarshal(frame.Params, &params); err != nil {
			return nil, err
		}
		source, format := params.selectors()
		if source == ReadRecent {
			return readResultDTO(s.terminal.ReadRecent(format)), nil
		}
		return readResultDTO(s.terminal.ReadVisible(format)), nil
	case "health":
		if err := s.journalOrder.gapError(time.Now(), s.gapTimeout); err != nil {
			return nil, err
		}
		if s.terminal == nil {
			return nil, errors.New("renderer is not initialized")
		}
		if !s.terminal.Health() {
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
		s.terminal.Output(event.ID, event.Data)
	case "resize":
		if event.CellWidth > 0 {
			if err := s.terminal.ResizePixels(event.Cols, event.Rows, event.CellWidth, event.CellHeight); err != nil {
				s.postEvent(tuiTerminalErrorEvent{TargetKey: s.terminalID, Err: err})
				_ = s.conn.Close()
				return
			}
			s.cellWidth, s.cellHeight = event.CellWidth, event.CellHeight
		} else {
			s.terminal.Resize(event.Cols, event.Rows)
		}
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
	if s.terminal == nil || s.replaying.Load() || s.journalGap.Load() {
		return
	}
	snapshot, visible, graphics, graphicsErr := s.terminal.PublishGraphics()
	if graphicsErr != nil {
		// Unsupported/over-limit graphics must not freeze the text terminal.
		// Clear the scene and keep the two text views coherent in a fresh read.
		snapshot, visible = s.terminal.Publish()
		graphics = terminalgraphics.Frame{}
	}
	if snapshot.Err != nil || visible.Err != nil {
		return
	}
	graphics.RendererGeneration = s.generation
	metadata, err := s.graphics.installOwned(graphics)
	if err != nil {
		graphicsErr = err
		metadata, _ = s.graphics.installOwned(terminalgraphics.Frame{RendererGeneration: s.generation})
	}
	if graphicsErr != nil && graphicsErr.Error() != s.lastGraphicsError {
		s.lastGraphicsError = graphicsErr.Error()
		s.postEvent(tuiTerminalErrorEvent{TargetKey: s.terminalID, Err: graphicsErr})
	} else if graphicsErr == nil {
		s.lastGraphicsError = ""
	}
	snapshot.Snapshot.Graphics = metadata
	state := rendererPublishedState{
		Snapshot:    snapshot.Snapshot,
		VisibleText: readResultDTO(visible),
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
	// Replay reconstructs emulator state; external effects belong only to the
	// live stream. Guard the entire effect sink so future callback types cannot
	// accidentally bypass the clipboard/notification policy.
	if s.replaying.Load() {
		return
	}
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
