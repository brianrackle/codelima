//go:build darwin || linux

package codelima

import (
	"encoding/json"
	"errors"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/terminalgraphics"
)

// GraphicsAsset proxies at most 64KiB of immutable pixels. The daemon keeps
// metadata only: the bounded worker cache owns the full 16MiB scene, avoiding
// a second full image store in every daemon terminal.
func (t *isolatedDaemonTerminal) GraphicsAsset(request daemon.TerminalGraphicsParams) (daemon.TerminalGraphicsChunk, error) {
	if _, err := t.graphicsAssetMetadata(request); err != nil {
		return daemon.TerminalGraphicsChunk{}, err
	}
	t.mu.Lock()
	renderer := t.renderer
	t.mu.Unlock()
	if renderer == nil {
		return daemon.TerminalGraphicsChunk{}, errRendererUnavailable
	}
	raw, err := renderer.call("graphics", request, renderer.options.CommandTimeout)
	if err != nil {
		return daemon.TerminalGraphicsChunk{}, err
	}
	var chunk daemon.TerminalGraphicsChunk
	if err := json.Unmarshal(raw, &chunk); err != nil {
		return daemon.TerminalGraphicsChunk{}, err
	}
	asset, err := t.graphicsAssetMetadata(request)
	if err != nil {
		return daemon.TerminalGraphicsChunk{}, err
	}
	length, _ := validateGraphicsRequest(request, int(asset.ByteLength))
	if chunk.RendererGeneration != request.RendererGeneration || chunk.ImageID != request.ImageID || chunk.Generation != request.Generation || chunk.Offset != request.Offset || chunk.Total != int(asset.ByteLength) || len(chunk.Data) != length {
		return daemon.TerminalGraphicsChunk{}, errors.New("renderer returned an invalid graphics chunk")
	}
	return chunk, nil
}

func (t *isolatedDaemonTerminal) graphicsAssetMetadata(request daemon.TerminalGraphicsParams) (terminalgraphics.Asset, error) {
	cache := t.cache.Load()
	if cache == nil || cache.state.Snapshot.Stale || cache.state.Snapshot.Graphics.RendererGeneration != request.RendererGeneration {
		return terminalgraphics.Asset{}, errors.New("graphics renderer generation is no longer available")
	}
	for _, asset := range cache.state.Snapshot.Graphics.Assets {
		if asset.ImageID == request.ImageID && asset.Generation == request.Generation {
			_, err := validateGraphicsRequest(request, int(asset.ByteLength))
			return asset, err
		}
	}
	return terminalgraphics.Asset{}, errors.New("graphics asset generation is no longer available")
}
