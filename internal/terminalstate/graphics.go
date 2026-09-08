package terminalstate

import (
	"errors"
	"github.com/brianrackle/codelima/internal/terminalgraphics"
)

var ErrCheckpointGraphics = errors.New("native checkpoint unavailable while images or image transfers are live; raw replay required")

// Graphics limits apply independently at native extraction and worker IPC.
const (
	GraphicsMaxBytes      = terminalgraphics.MaxBytes
	GraphicsMaxPixels     = terminalgraphics.MaxPixels
	GraphicsMaxAssets     = terminalgraphics.MaxAssets
	GraphicsMaxPlacements = terminalgraphics.MaxPlacements
)

// GraphicsFrame is a complete visible static placement set. Generation tracks
// storage content, not geometry: scrolling can move unchanged assets.
type GraphicsFrame = terminalgraphics.Frame

type GraphicsAsset = terminalgraphics.Asset

type GraphicsPlacement = terminalgraphics.Placement
