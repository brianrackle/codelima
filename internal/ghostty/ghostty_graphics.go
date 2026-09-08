//go:build cgo && (darwin || linux)

package ghostty

// #include "ghostty_bridge_compat.h"
import "C"

import (
	"errors"
	"fmt"
	"slices"
	"unsafe"

	"github.com/brianrackle/codelima/internal/terminalstate"
)

type cmdGraphics struct{ Reply chan graphicsOutcome }
type graphicsOutcome struct {
	Frame terminalstate.GraphicsFrame
	Err   error
}

type cmdPublishGraphics struct{ Reply chan graphicsPublication }
type graphicsPublication struct {
	Snapshot terminalstate.SnapshotResult
	Visible  terminalstate.ReadResult
	Graphics terminalstate.GraphicsFrame
	Err      error
}

// PublishGraphics captures text, cells, and images in one actor turn, marking
// damage clean only after all owned outputs have been constructed successfully.
func (t *ghosttyTUITerminal) PublishGraphics() (terminalstate.SnapshotResult, terminalstate.ReadResult, terminalstate.GraphicsFrame, error) {
	reply := make(chan graphicsPublication, 1)
	select {
	case t.commands <- actorEnvelope{cmd: cmdPublishGraphics{Reply: reply}}:
	case <-t.actorDone:
		return terminalstate.SnapshotResult{}, terminalstate.ReadResult{}, terminalstate.GraphicsFrame{}, errTerminalClosed
	}
	select {
	case result := <-reply:
		return result.Snapshot, result.Visible, result.Graphics, result.Err
	case <-t.actorDone:
		return terminalstate.SnapshotResult{}, terminalstate.ReadResult{}, terminalstate.GraphicsFrame{}, errTerminalClosed
	}
}

func (t *ghosttyTUITerminal) publishGraphicsLocked() graphicsPublication {
	t.mu.Lock()
	defer t.mu.Unlock()
	var output graphicsPublication
	output.Graphics, output.Err = t.graphicsLockedRaw()
	if output.Err != nil {
		return output
	}
	output.Snapshot = t.captureSnapshotLockedRaw()
	if output.Snapshot.Err != nil {
		output.Err = output.Snapshot.Err
		return output
	}
	output.Visible.Text, output.Err = t.formatLockedRaw(ReadVisible, ReadText)
	output.Visible.Generation, output.Visible.Err = t.generation, output.Err
	if output.Err == nil {
		C.ghostty_bridge_render_state_mark_clean(t.term)
		output.Err = t.checkNativeErrorLockedRaw("publish graphics")
	}
	return output
}

type cmdResizePixels struct {
	Cols, Rows, CellWidth, CellHeight int
	Reply                             chan error
}

func (t *ghosttyTUITerminal) Graphics() (terminalstate.GraphicsFrame, error) {
	reply := make(chan graphicsOutcome, 1)
	select {
	case t.commands <- actorEnvelope{cmd: cmdGraphics{Reply: reply}}:
	case <-t.actorDone:
		return terminalstate.GraphicsFrame{}, errTerminalClosed
	}
	select {
	case result := <-reply:
		return result.Frame, result.Err
	case <-t.actorDone:
		return terminalstate.GraphicsFrame{}, errTerminalClosed
	}
}

// ResizePixels accepts the outer terminal's measured cell geometry. The native
// engine uses this for image placement; omitted geometry retains its last value.
func (t *ghosttyTUITerminal) ResizePixels(cols, rows, cellWidth, cellHeight int) error {
	if cols <= 0 || rows <= 0 || cols > 65535 || rows > 65535 || cellWidth <= 0 || cellHeight <= 0 || cellWidth > 4096 || cellHeight > 4096 {
		return errors.New("invalid terminal pixel geometry")
	}
	reply := make(chan error, 1)
	select {
	case t.commands <- actorEnvelope{cmd: cmdResizePixels{Cols: cols, Rows: rows, CellWidth: cellWidth, CellHeight: cellHeight, Reply: reply}}:
	case <-t.actorDone:
		return errTerminalClosed
	}
	select {
	case err := <-reply:
		return err
	case <-t.actorDone:
		return errTerminalClosed
	}
}

func (t *ghosttyTUITerminal) resizePixels(command cmdResizePixels) error {
	t.applyResize(command.Cols, command.Rows)
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.term == nil {
		return errTerminalClosed
	}
	result := C.ghostty_bridge_terminal_resize_pixels(t.term, C.int(command.Cols), C.int(command.Rows), C.uint32_t(command.CellWidth), C.uint32_t(command.CellHeight))
	if result != C.GHOSTTY_SUCCESS {
		return fmt.Errorf("resize terminal pixels: native result %d", int(result))
	}
	t.generation++
	t.invalidateLocked()
	return nil
}

func (t *ghosttyTUITerminal) graphicsLocked() (terminalstate.GraphicsFrame, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.graphicsLockedRaw()
}

func (t *ghosttyTUITerminal) graphicsLockedRaw() (terminalstate.GraphicsFrame, error) {
	var output terminalstate.GraphicsFrame
	if t.closed || t.term == nil {
		return output, errTerminalClosed
	}
	if t.nativeErr != nil {
		return output, t.nativeErr
	}
	var frame C.GhosttyBridgeGraphicsFrame
	result := C.ghostty_bridge_terminal_graphics(t.term, C.size_t(terminalstate.GraphicsMaxBytes), &frame) //nolint:gocritic // cgo generates duplicate pointer-safety expressions.
	defer C.ghostty_bridge_graphics_frame_free(&frame)                                                     //nolint:gocritic // cgo-generated pointer-safety expression, not duplicated application logic.
	if result != C.GHOSTTY_SUCCESS {
		return output, fmt.Errorf("terminal graphics: native result %d", int(result))
	}
	if frame.assets_len > terminalstate.GraphicsMaxAssets || frame.placements_len > terminalstate.GraphicsMaxPlacements || frame.rgba_len > terminalstate.GraphicsMaxBytes ||
		(frame.assets_len != 0 && frame.assets == nil) || (frame.placements_len != 0 && frame.placements == nil) || (frame.rgba_len != 0 && frame.rgba == nil) {
		return output, errors.New("invalid native graphics frame")
	}
	output.Generation = uint64(frame.generation)
	nativeAssets := unsafe.Slice(frame.assets, int(frame.assets_len))
	nativePlacements := unsafe.Slice(frame.placements, int(frame.placements_len))
	pixels := unsafe.Slice((*byte)(unsafe.Pointer(frame.rgba)), int(frame.rgba_len))
	output.Assets = make([]terminalstate.GraphicsAsset, len(nativeAssets))
	for index, asset := range nativeAssets {
		if asset.width == 0 || asset.height == 0 || uint64(asset.width)*uint64(asset.height) > terminalstate.GraphicsMaxPixels ||
			uint64(asset.rgba_len) != uint64(asset.width)*uint64(asset.height)*4 || asset.rgba_offset > frame.rgba_len || asset.rgba_len > frame.rgba_len-asset.rgba_offset {
			return terminalstate.GraphicsFrame{}, errors.New("invalid native graphics asset")
		}
		output.Assets[index] = terminalstate.GraphicsAsset{
			ImageID: uint32(asset.image_id), Generation: uint64(asset.generation), Width: uint32(asset.width), Height: uint32(asset.height), ByteLength: uint32(asset.rgba_len),
			RGBA: slices.Clone(pixels[int(asset.rgba_offset):int(asset.rgba_offset+asset.rgba_len)]),
		}
	}
	output.Placements = make([]terminalstate.GraphicsPlacement, len(nativePlacements))
	for index, placement := range nativePlacements {
		if uint64(placement.asset_index) >= uint64(len(output.Assets)) {
			return terminalstate.GraphicsFrame{}, errors.New("invalid native graphics asset reference")
		}
		asset := output.Assets[int(placement.asset_index)]
		if asset.ImageID != uint32(placement.image_id) {
			return terminalstate.GraphicsFrame{}, errors.New("native graphics image reference mismatch")
		}
		geometry := placement.geometry
		output.Placements[index] = terminalstate.GraphicsPlacement{
			ImageID: uint32(placement.image_id), PlacementID: uint32(placement.placement_id), AssetGeneration: asset.Generation,
			Z: int32(placement.z), OffsetX: uint32(placement.offset_x), OffsetY: uint32(placement.offset_y),
			PixelWidth: uint32(geometry.pixel_width), PixelHeight: uint32(geometry.pixel_height), GridCols: uint32(geometry.grid_cols), GridRows: uint32(geometry.grid_rows),
			ViewportCol: int32(geometry.viewport_col), ViewportRow: int32(geometry.viewport_row),
			SourceX: uint32(geometry.source_x), SourceY: uint32(geometry.source_y), SourceWidth: uint32(geometry.source_width), SourceHeight: uint32(geometry.source_height),
		}
	}
	return output, nil
}
