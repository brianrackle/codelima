package codelima

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"go.rockorager.dev/vaxis"
	"image"
	"image/png"
	"io"
	"log/slog"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/terminalgraphics"
)

type tuiGraphicsKey struct {
	Epoch                             uint64
	Target                            string
	Digest                            [32]byte
	Cols, Rows, CellWidth, CellHeight int
}
type tuiGraphicsRequest struct {
	key      tuiGraphicsKey
	frame    terminalgraphics.Frame
	terminal *daemonTUITerminal
}
type tuiGraphicsSprite struct {
	Col, Row, Z, Width, Height int
	PNG                        []byte
}
type tuiGraphicsResult struct {
	key     tuiGraphicsKey
	sprites []tuiGraphicsSprite
	err     error
}

// One presenter belongs to the frontend, not each tab. Its latest-value queue,
// asset cache and raster budget bound memory even with many hidden terminals.
type tuiGraphicsPresenter struct {
	requests   chan tuiGraphicsRequest
	results    chan tuiGraphicsResult
	cancel     context.CancelFunc
	done       chan struct{}
	once       sync.Once
	requested  tuiGraphicsKey
	installed  tuiGraphicsKey
	images     []*vaxis.EncodedKittyImage
	sprites    []tuiGraphicsSprite
	visible    bool
	tick       *time.Timer
	retryAfter time.Time
	retryKey   tuiGraphicsKey
	retryCount int
}

func newTUIGraphicsPresenter() *tuiGraphicsPresenter {
	ctx, cancel := context.WithCancel(context.Background())
	p := &tuiGraphicsPresenter{requests: make(chan tuiGraphicsRequest, 1), results: make(chan tuiGraphicsResult, 1), cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(p.done)
		cache := make(map[graphicsAssetKey][]byte)
		for {
			select {
			case <-ctx.Done():
				return
			case request := <-p.requests:
				result := tuiGraphicsResult{key: request.key}
				workCtx, cancelWork := context.WithTimeout(ctx, 5*time.Second)
				result.sprites, result.err = prepareTUIGraphics(workCtx, request, cache)
				cancelWork()
				select {
				case p.results <- result:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return p
}

func (p *tuiGraphicsPresenter) close() {
	if p != nil {
		p.clearImages()
		if p.tick != nil {
			p.tick.Stop()
		}
		p.once.Do(p.cancel)
		<-p.done
	}
}

func (p *tuiGraphicsPresenter) clearImages() {
	for _, img := range p.images {
		img.Destroy()
	}
	p.images = nil
	p.sprites = nil
	p.installed = tuiGraphicsKey{}
}

func (a *vaxisTUIApp) graphicsResults() <-chan tuiGraphicsResult {
	if a.graphics == nil {
		return nil
	}
	return a.graphics.results
}
func (a *vaxisTUIApp) graphicsTicks() <-chan time.Time {
	if a.graphics == nil || a.graphics.tick == nil {
		return nil
	}
	return a.graphics.tick.C
}

func (a *vaxisTUIApp) applyGraphicsResult(result tuiGraphicsResult) {
	p := a.graphics
	if p == nil || result.key != p.requested {
		return
	}
	if result.err != nil {
		a.setStatus(slog.LevelError, result.err.Error())
		if p.retryKey != result.key {
			p.retryKey = result.key
			p.retryCount = 0
		}
		p.retryCount++
		if p.retryCount <= 3 {
			p.requested = tuiGraphicsKey{}
			p.retryAfter = time.Now().Add(time.Second)
			if p.tick != nil {
				p.tick.Stop()
			}
			p.tick = time.NewTimer(time.Second)
		}
		return
	}
	p.clearImages()
	for _, sprite := range result.sprites {
		img, err := a.vx.NewEncodedKittyImage(sprite.PNG, sprite.Width, sprite.Height)
		if err != nil {
			p.clearImages()
			a.setStatus(slog.LevelError, err.Error())
			return
		}
		p.images = append(p.images, img)
	}
	for i := range result.sprites {
		result.sprites[i].PNG = nil
	}
	p.sprites = result.sprites
	p.installed = result.key
	p.retryCount = 0
	p.retryKey = tuiGraphicsKey{}
}

func (a *vaxisTUIApp) drawTerminalGraphics(win vaxis.Window, term *daemonTUITerminal) {
	if a.vx == nil || !a.vx.CanKittyGraphics() || a.cellPixelWidth <= 0 || a.cellPixelHeight <= 0 || a.overlay != nil {
		return
	}
	cols, rows := win.Size()
	if a.search != nil {
		rows -= 2
	}
	if rows <= 0 || cols <= 0 {
		return
	}
	term.resizePixels(cols, win.Height, a.cellPixelWidth, a.cellPixelHeight)
	term.mu.RLock()
	frame := term.snapshot.Graphics
	term.mu.RUnlock()
	if len(frame.Placements) == 0 {
		return
	}
	if a.graphics == nil {
		a.graphics = newTUIGraphicsPresenter()
	}
	p := a.graphics
	p.visible = true
	key := tuiGraphicsSceneKey(term.id, frame, cols, rows, a.cellPixelWidth, a.cellPixelHeight)
	key.Epoch = a.graphicsEpoch
	if key == p.retryKey && time.Now().Before(p.retryAfter) {
		return
	}
	if p.requested != key {
		p.clearImages()
		p.requested = key
		request := tuiGraphicsRequest{key: key, frame: frame, terminal: term}
		select {
		case <-p.requests:
		default:
		}
		p.requests <- request
	}
	if p.installed != key {
		return
	}
	remaining := terminalgraphics.MaxChunkBytes
	pending := false
	for index, img := range p.images {
		used, done := img.Upload(remaining)
		remaining -= used
		if !done {
			pending = true
			continue
		}
		if err := img.Err(); err != nil {
			a.setStatus(slog.LevelError, err.Error())
			p.clearImages()
			return
		}
		sprite := p.sprites[index]
		img.DrawAtZ(win.New(sprite.Col, sprite.Row, cols-sprite.Col, rows-sprite.Row), int32(sprite.Z))
	}
	if pending && p.tick == nil {
		p.tick = time.NewTimer(50 * time.Millisecond)
	}
}

func (a *vaxisTUIApp) resetGraphicsEpoch() {
	a.graphicsEpoch++
	if p := a.graphics; p != nil {
		p.clearImages()
		p.requested = tuiGraphicsKey{}
		p.retryCount = 0
		p.retryKey = tuiGraphicsKey{}
		if p.tick != nil {
			p.tick.Stop()
			p.tick = nil
		}
		select {
		case <-p.requests:
		default:
		}
	}
}

func (a *vaxisTUIApp) beginGraphicsFrame() {
	if a.graphics == nil {
		return
	}
	a.graphics.visible = false
	for _, img := range a.graphics.images {
		a.vx.RemoveImage(img)
	}
}

func (a *vaxisTUIApp) finishGraphicsFrame() {
	p := a.graphics
	if p != nil && !p.visible {
		p.clearImages()
		p.requested = tuiGraphicsKey{}
	}
}

type graphicsAssetKey struct {
	Epoch      uint64
	Terminal   string
	Renderer   uint64
	Image      uint32
	Generation uint64
}

func fetchTUIGraphicsAsset(ctx context.Context, term *daemonTUITerminal, frame terminalgraphics.Frame, asset terminalgraphics.Asset) ([]byte, error) {
	if !validTUIGraphicsAsset(asset) {
		return nil, errors.New("invalid graphics asset dimensions")
	}
	data := make([]byte, 0, asset.ByteLength)
	for len(data) < int(asset.ByteLength) {
		callCtx, cancel := context.WithTimeout(ctx, daemonRPCTimeout)
		params := daemon.TerminalGraphicsParams{TerminalID: term.id, RendererGeneration: frame.RendererGeneration, ImageID: asset.ImageID, Generation: asset.Generation, Offset: len(data), Length: min(terminalgraphics.MaxChunkBytes, int(asset.ByteLength)-len(data))}
		var chunk daemon.TerminalGraphicsChunk
		err := term.client.Call(callCtx, "terminal.graphics", params, &chunk)
		cancel()
		if err != nil {
			return nil, err
		}
		if chunk.RendererGeneration != params.RendererGeneration || chunk.ImageID != params.ImageID || chunk.Generation != params.Generation || chunk.Offset != params.Offset || chunk.Total != int(asset.ByteLength) || len(chunk.Data) != params.Length {
			return nil, errors.New("graphics chunk identity or bounds mismatch")
		}
		data = append(data, chunk.Data...)
	}
	return data, nil
}

func validTUIGraphicsAsset(asset terminalgraphics.Asset) bool {
	return asset.Width > 0 && asset.Height > 0 && asset.Width <= terminalgraphics.MaxPixels/asset.Height && uint64(asset.Width)*uint64(asset.Height)*4 == uint64(asset.ByteLength) && asset.ByteLength <= terminalgraphics.MaxBytes
}

type graphicsLimitedWriter struct {
	buffer    bytes.Buffer
	remaining int
}

func (w *graphicsLimitedWriter) Write(data []byte) (int, error) {
	if len(data) > w.remaining {
		return 0, io.ErrShortWrite
	}
	w.remaining -= len(data)
	return w.buffer.Write(data)
}

func prepareTUIGraphics(ctx context.Context, request tuiGraphicsRequest, cache map[graphicsAssetKey][]byte) ([]tuiGraphicsSprite, error) {
	frame, key := request.frame, request.key
	if len(frame.Assets) > terminalgraphics.MaxAssets || len(frame.Placements) > terminalgraphics.MaxPlacements || key.CellWidth <= 0 || key.CellHeight <= 0 || key.CellWidth > 4096 || key.CellHeight > 4096 || key.Cols <= 0 || key.Rows <= 0 || key.Cols > 65535 || key.Rows > 65535 {
		return nil, errors.New("invalid graphics scene bounds")
	}
	assets := make(map[uint32]terminalgraphics.Asset, len(frame.Assets))
	wanted := make(map[graphicsAssetKey]bool, len(frame.Assets))
	total := uint64(0)
	for _, asset := range frame.Assets {
		if _, duplicate := assets[asset.ImageID]; duplicate || !validTUIGraphicsAsset(asset) {
			return nil, errors.New("invalid or duplicate graphics asset")
		}
		total += uint64(asset.ByteLength)
		if total > terminalgraphics.MaxBytes {
			return nil, errors.New("graphics asset cache budget exceeded")
		}
		assets[asset.ImageID] = asset
		wanted[graphicsAssetKey{key.Epoch, key.Target, frame.RendererGeneration, asset.ImageID, asset.Generation}] = true
	}
	for identity := range cache {
		if !wanted[identity] {
			delete(cache, identity)
		}
	}
	placements := slices.Clone(frame.Placements)
	sort.SliceStable(placements, func(i, j int) bool { return placements[i].Z < placements[j].Z })
	var sprites []tuiGraphicsSprite
	rasterBytes, encodedBytes := 0, 0
	for _, placement := range placements {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		asset, ok := assets[placement.ImageID]
		if !ok || asset.Generation != placement.AssetGeneration {
			return nil, errors.New("graphics placement asset mismatch")
		}
		identity := graphicsAssetKey{key.Epoch, key.Target, frame.RendererGeneration, asset.ImageID, asset.Generation}
		data, ok := cache[identity]
		if !ok {
			var err error
			data, err = fetchTUIGraphicsAsset(ctx, request.terminal, frame, asset)
			if err != nil {
				return nil, err
			}
			cache[identity] = data
		}
		img, col, row, err := rasterTUIGraphics(ctx, key, asset, placement, data, terminalgraphics.MaxBytes-rasterBytes)
		if err != nil {
			return nil, err
		}
		if img == nil {
			continue
		}
		rasterBytes += len(img.Pix)
		encoded := graphicsLimitedWriter{remaining: terminalgraphics.MaxBytes - encodedBytes}
		if err := png.Encode(&encoded, img); err != nil {
			return nil, err
		}
		encodedBytes += encoded.buffer.Len()
		if encodedBytes > terminalgraphics.MaxBytes {
			return nil, errors.New("graphics encoded scene budget exceeded")
		}
		sprites = append(sprites, tuiGraphicsSprite{Col: col, Row: row, Z: int(placement.Z), Width: img.Rect.Dx(), Height: img.Rect.Dy(), PNG: encoded.buffer.Bytes()})
	}
	return sprites, nil
}

// Rasterize only the visible intersection, including a transparent sub-cell
// offset. Clipping happens before allocation; untrusted off-screen dimensions
// cannot allocate their full surface. Source coordinates use straight RGBA.
func rasterTUIGraphics(ctx context.Context, key tuiGraphicsKey, asset terminalgraphics.Asset, p terminalgraphics.Placement, data []byte, budget int) (*image.NRGBA, int, int, error) {
	if !validTUIGraphicsAsset(asset) || len(data) != int(asset.ByteLength) || key.CellWidth <= 0 || key.CellHeight <= 0 || key.CellWidth > 4096 || key.CellHeight > 4096 || key.Cols <= 0 || key.Rows <= 0 || key.Cols > 65535 || key.Rows > 65535 || budget < 0 {
		return nil, 0, 0, errors.New("invalid graphics raster geometry")
	}
	cw, ch := int64(key.CellWidth), int64(key.CellHeight)
	width, height := int64(p.PixelWidth), int64(p.PixelHeight)
	x, y := int64(p.ViewportCol)*cw+int64(p.OffsetX), int64(p.ViewportRow)*ch+int64(p.OffsetY)
	left, top := max(x, 0), max(y, 0)
	right, bottom := min(x+width, int64(key.Cols)*cw), min(y+height, int64(key.Rows)*ch)
	if width <= 0 || height <= 0 || right <= left || bottom <= top {
		return nil, 0, 0, nil
	}
	sx, sy, sw, sh := int64(p.SourceX), int64(p.SourceY), int64(p.SourceWidth), int64(p.SourceHeight)
	if sw == 0 {
		sw = int64(asset.Width) - sx
	}
	if sh == 0 {
		sh = int64(asset.Height) - sy
	}
	if sx < 0 || sy < 0 || sw <= 0 || sh <= 0 || sx+sw > int64(asset.Width) || sy+sh > int64(asset.Height) || uint64(len(data)) != uint64(asset.Width)*uint64(asset.Height)*4 {
		return nil, 0, 0, errors.New("invalid graphics source rectangle")
	}
	originX, originY := left/cw*cw, top/ch*ch
	w, h := right-originX, bottom-originY
	if w <= 0 || h <= 0 || w > int64(budget)/4/h {
		return nil, 0, 0, errors.New("graphics raster budget exceeded")
	}
	img := image.NewNRGBA(image.Rect(0, 0, int(w), int(h)))
	for dy := top; dy < bottom; dy++ {
		if err := ctx.Err(); err != nil {
			return nil, 0, 0, err
		}
		sourceY := sy + (dy-y)*sh/height
		for dx := left; dx < right; dx++ {
			sourceX := sx + (dx-x)*sw/width
			src := (sourceY*int64(asset.Width) + sourceX) * 4
			dst := ((dy-originY)*w + dx - originX) * 4
			copy(img.Pix[dst:dst+4], data[src:src+4])
		}
	}
	return img, int(originX / cw), int(originY / ch), nil
}

func tuiGraphicsSceneKey(target string, frame terminalgraphics.Frame, cols, rows, cw, ch int) tuiGraphicsKey {
	// Overall text render generations are deliberately excluded: only image
	// identity, placement or geometry changes invalidate a prepared scene.
	frame.Generation = 0
	encoded, _ := json.Marshal(frame)
	return tuiGraphicsKey{Target: target, Digest: sha256.Sum256(encoded), Cols: cols, Rows: rows, CellWidth: cw, CellHeight: ch}
}
