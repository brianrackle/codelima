// Package terminalgraphics defines bounded, native-independent static image
// scenes and immutable asset identities shared by daemon and terminal clients.
package terminalgraphics

const (
	MaxBytes      = 16 << 20
	MaxPixels     = 4 << 20
	MaxAssets     = 256
	MaxPlacements = 1024
	MaxChunkBytes = 64 << 10
)

type Frame struct {
	RendererGeneration uint64      `json:"renderer_generation"`
	Generation         uint64      `json:"generation"`
	Assets             []Asset     `json:"assets,omitempty"`
	Placements         []Placement `json:"placements,omitempty"`
}

type Asset struct {
	ImageID    uint32 `json:"image_id"`
	Generation uint64 `json:"generation"`
	Width      uint32 `json:"width"`
	Height     uint32 `json:"height"`
	ByteLength uint32 `json:"byte_length"`
	RGBA       []byte `json:"-"`
}

type Placement struct {
	ImageID         uint32 `json:"image_id"`
	PlacementID     uint32 `json:"placement_id"`
	AssetGeneration uint64 `json:"asset_generation"`
	Z               int32  `json:"z"`
	OffsetX         uint32 `json:"offset_x"`
	OffsetY         uint32 `json:"offset_y"`
	PixelWidth      uint32 `json:"pixel_width"`
	PixelHeight     uint32 `json:"pixel_height"`
	GridCols        uint32 `json:"grid_cols"`
	GridRows        uint32 `json:"grid_rows"`
	ViewportCol     int32  `json:"viewport_col"`
	ViewportRow     int32  `json:"viewport_row"`
	SourceX         uint32 `json:"source_x"`
	SourceY         uint32 `json:"source_y"`
	SourceWidth     uint32 `json:"source_width"`
	SourceHeight    uint32 `json:"source_height"`
}
