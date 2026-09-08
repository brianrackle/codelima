//go:build cgo && (darwin || linux)

package ghostty

// #include "ghostty_bridge_compat.h"
import "C"

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"unsafe"
)

const (
	ghosttyPNGMaxBytes  = 16 * 1024 * 1024
	ghosttyPNGMaxPixels = 4 * 1024 * 1024
)

// decodeGhosttyPNG checks dimensions before PNG allocates decoded pixel data.
// The image decoder is the only graphics parsing owned by Go: Ghostty handles
// Kitty framing, chunking, compression, storage, placements, and deletion.
func decodeGhosttyPNG(data []byte) (image.Image, error) {
	if len(data) == 0 || len(data) > ghosttyPNGMaxBytes {
		return nil, errors.New("PNG input exceeds native graphics bounds")
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if config.Width <= 0 || config.Height <= 0 || uint64(config.Width)*uint64(config.Height) > ghosttyPNGMaxPixels {
		return nil, errors.New("PNG dimensions exceed native graphics bounds")
	}
	decoded, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if decoded.Bounds().Dx() != config.Width || decoded.Bounds().Dy() != config.Height {
		return nil, errors.New("PNG dimensions changed during decode")
	}
	return decoded, nil
}

// writeGhosttyRGBA writes straight (not premultiplied) RGBA into an exactly
// sized buffer supplied by the native allocator. No Go pointer escapes.
func writeGhosttyRGBA(dst []byte, decoded image.Image) {
	bounds := decoded.Bounds()
	index := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			pixel := color.NRGBAModel.Convert(decoded.At(x, y)).(color.NRGBA)
			dst[index], dst[index+1], dst[index+2], dst[index+3] = pixel.R, pixel.G, pixel.B, pixel.A
			index += 4
		}
	}
}

//export codelimaGhosttyDecodePNG
func codelimaGhosttyDecodePNG(allocator *C.GhosttyAllocator, data *C.uint8_t, size C.size_t, out *C.GhosttySysImage) C.bool {
	if data == nil || out == nil || size == 0 || uint64(size) > ghosttyPNGMaxBytes {
		return C.bool(false)
	}
	decoded, err := decodeGhosttyPNG(unsafe.Slice((*byte)(unsafe.Pointer(data)), int(size)))
	if err != nil {
		return C.bool(false)
	}
	bounds := decoded.Bounds()
	if C.ghostty_bridge_png_alloc(allocator, C.uint32_t(bounds.Dx()), C.uint32_t(bounds.Dy()), out) != C.GHOSTTY_SUCCESS {
		return C.bool(false)
	}
	writeGhosttyRGBA(unsafe.Slice((*byte)(unsafe.Pointer(out.data)), int(out.data_len)), decoded)
	return C.bool(true)
}
