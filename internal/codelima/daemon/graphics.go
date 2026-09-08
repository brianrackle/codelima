package daemon

type TerminalGraphicsParams struct {
	TerminalID         string `json:"terminal_id"`
	RendererGeneration uint64 `json:"renderer_generation"`
	ImageID            uint32 `json:"image_id"`
	Generation         uint64 `json:"generation"`
	Offset             int    `json:"offset"`
	Length             int    `json:"length"`
}

type TerminalGraphicsChunk struct {
	RendererGeneration uint64 `json:"renderer_generation"`
	ImageID            uint32 `json:"image_id"`
	Generation         uint64 `json:"generation"`
	Offset             int    `json:"offset"`
	Total              int    `json:"total"`
	Data               []byte `json:"data"`
}
