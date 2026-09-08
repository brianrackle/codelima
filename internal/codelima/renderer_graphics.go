package codelima

import (
	"errors"
	"slices"
	"sync"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/terminalgraphics"
)

type rendererGraphicsAssets struct {
	mu         sync.Mutex
	generation uint64
	assets     map[uint32]terminalgraphics.Asset
}

// installOwned consumes native-owned pixels. Only metadata crosses snapshot
// publication; callers must not mutate the consumed frame afterwards. Reads
// return copies, so no RPC client receives a mutable view of this cache.
func (c *rendererGraphicsAssets) installOwned(frame terminalgraphics.Frame) (terminalgraphics.Frame, error) {
	if err := validateRendererGraphics(frame, true); err != nil {
		return terminalgraphics.Frame{}, err
	}
	assets := make(map[uint32]terminalgraphics.Asset, len(frame.Assets))
	metadata := cloneGraphicsMetadata(frame)
	for _, asset := range frame.Assets {
		assets[asset.ImageID] = asset
	}
	c.mu.Lock()
	c.generation, c.assets = frame.RendererGeneration, assets
	c.mu.Unlock()
	return metadata, nil
}

func cloneGraphicsMetadata(frame terminalgraphics.Frame) terminalgraphics.Frame {
	frame.Assets = slices.Clone(frame.Assets)
	frame.Placements = slices.Clone(frame.Placements)
	for index := range frame.Assets {
		frame.Assets[index].RGBA = nil
	}
	return frame
}

func validateRendererGraphics(frame terminalgraphics.Frame, pixels bool) error {
	if len(frame.Assets) > terminalgraphics.MaxAssets || len(frame.Placements) > terminalgraphics.MaxPlacements {
		return errors.New("graphics scene exceeds its asset or placement bound")
	}
	assets := make(map[uint32]terminalgraphics.Asset, len(frame.Assets))
	total := uint64(0)
	for _, asset := range frame.Assets {
		// Bound the pixel product before multiplying by four: hostile uint32
		// dimensions can otherwise wrap an apparently valid uint64 byte size.
		if asset.Width == 0 || asset.Height == 0 || uint64(asset.Width) > terminalgraphics.MaxPixels/uint64(asset.Height) {
			return errors.New("graphics asset dimensions exceed the pixel bound")
		}
		length := uint64(asset.Width) * uint64(asset.Height) * 4
		if asset.ImageID == 0 || asset.Width == 0 || asset.Height == 0 || length > terminalgraphics.MaxBytes || length != uint64(asset.ByteLength) || (pixels && uint64(len(asset.RGBA)) != length) {
			return errors.New("graphics asset bounds are invalid")
		}
		if _, duplicate := assets[asset.ImageID]; duplicate {
			return errors.New("graphics scene contains a duplicate asset")
		}
		total += length
		if total > terminalgraphics.MaxBytes {
			return errors.New("graphics scene exceeds aggregate pixel bound")
		}
		assets[asset.ImageID] = asset
	}
	for _, placement := range frame.Placements {
		asset, ok := assets[placement.ImageID]
		if !ok || placement.AssetGeneration != asset.Generation {
			return errors.New("graphics placement references an unavailable asset generation")
		}
		if placement.SourceX > asset.Width || placement.SourceY > asset.Height || placement.SourceWidth > asset.Width-placement.SourceX || placement.SourceHeight > asset.Height-placement.SourceY {
			return errors.New("graphics placement source bounds are invalid")
		}
	}
	return nil
}

func validateGraphicsRequest(request daemon.TerminalGraphicsParams, total int) (int, error) {
	if request.Offset < 0 || request.Offset > total || request.Length <= 0 || request.Length > terminalgraphics.MaxChunkBytes {
		return 0, errors.New("graphics chunk offset or length is outside its bound")
	}
	return min(request.Length, total-request.Offset), nil
}

func (c *rendererGraphicsAssets) read(request daemon.TerminalGraphicsParams) (daemon.TerminalGraphicsChunk, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	asset, ok := c.assets[request.ImageID]
	if !ok || request.RendererGeneration != c.generation || request.Generation != asset.Generation {
		return daemon.TerminalGraphicsChunk{}, errors.New("graphics asset generation is no longer available")
	}
	length, err := validateGraphicsRequest(request, len(asset.RGBA))
	if err != nil {
		return daemon.TerminalGraphicsChunk{}, err
	}
	return daemon.TerminalGraphicsChunk{RendererGeneration: c.generation, ImageID: asset.ImageID, Generation: asset.Generation, Offset: request.Offset, Total: len(asset.RGBA), Data: slices.Clone(asset.RGBA[request.Offset : request.Offset+length])}, nil
}
