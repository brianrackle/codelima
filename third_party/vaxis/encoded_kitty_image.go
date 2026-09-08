// CodeLima extension to Vaxis v0.17.1. Licensed under Apache-2.0; see LICENSE.

package vaxis

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image/png"
	"io"
	"slices"
)

const (
	maxEncodedKittyBytes  = 16 << 20
	maxEncodedKittyPixels = 4 << 20
	kittyRawChunkBytes    = 3072 // at most 4096 base64 payload bytes
)

// EncodedKittyImage owns one already-rasterized PNG. Unlike KittyImage, it
// never starts encoding/resizing goroutines or scales pixels to cell geometry.
// Call Upload with a per-frame wire-byte budget before drawing. Like Window
// drawing, lifecycle operations belong to the Vaxis UI owner.
type EncodedKittyImage struct {
	vx                  *Vaxis
	id                  uint64
	png                 []byte
	width, height       int
	offset              int
	uploaded, destroyed bool
	err                 error
}

var _ Image = (*EncodedKittyImage)(nil)

// NewEncodedKittyImage validates the PNG header and exact pixel dimensions,
// then clones data. The caller must provide a complete, already-encoded PNG;
// no decoding allocation or host write occurs here. Data is limited to 16 MiB
// and four million pixels. No fallback protocol or background work is started.
func (vx *Vaxis) NewEncodedKittyImage(data []byte, pixelW, pixelH int) (*EncodedKittyImage, error) {
	if vx == nil || len(data) == 0 || len(data) > maxEncodedKittyBytes || pixelW <= 0 || pixelH <= 0 || pixelW > maxEncodedKittyPixels/pixelH {
		return nil, errors.New("encoded Kitty image exceeds byte or pixel bounds")
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width != pixelW || config.Height != pixelH {
		return nil, errors.New("encoded Kitty image PNG dimensions do not match")
	}
	vx.mu.Lock()
	defer vx.mu.Unlock()
	if vx.closed || vx.graphicsProtocol != kitty || vx.tw == nil || vx.tw.terminal == nil || vx.tw.terminal.w == nil {
		return nil, errors.New("Kitty graphics transport is unavailable")
	}
	if vx.graphicsIDNext >= 1<<32-1 {
		return nil, errors.New("Kitty image identifiers exhausted")
	}
	return &EncodedKittyImage{vx: vx, id: vx.nextGraphicID(), png: slices.Clone(data), width: pixelW, height: pixelH}, nil
}

// Upload synchronously writes at most maxBytes bytes, including framing and
// base64 expansion, through Vaxis's terminal write lock. It returns the wire
// bytes consumed and whether no work remains. Too-small/nonpositive budgets,
// or another active multipart image, return (0,false). Transfers never
// interleave on a surface. A failed write ends the transfer; inspect Err when
// done is true. This bounds work/bytes, not a stalled host terminal's latency.
func (k *EncodedKittyImage) Upload(maxBytes int) (consumed int, done bool) {
	k.vx.mu.Lock()
	defer k.vx.mu.Unlock()
	if k.destroyed || k.uploaded || k.err != nil {
		return 0, true
	}
	if maxBytes <= 0 || (k.vx.encodedUpload != nil && k.vx.encodedUpload != k) {
		return 0, false
	}
	for consumed < maxBytes {
		header := "\x1b_Gq=2,m=1;"
		if k.offset == 0 {
			header = fmt.Sprintf("\x1b_Ga=t,t=d,f=100,s=%d,v=%d,i=%d,q=2,m=1;", k.width, k.height, k.id)
		}
		capacity := (maxBytes - consumed - len(header) - 2) / 4 * 3
		if capacity <= 0 {
			return consumed, false
		}
		rawBytes := min(capacity, kittyRawChunkBytes, len(k.png)-k.offset)
		last := k.offset+rawBytes == len(k.png)
		if last {
			// Both m values have the same wire length.
			header = header[:len(header)-2] + "0;"
		}
		packet := make([]byte, len(header)+base64.StdEncoding.EncodedLen(rawBytes)+2)
		copy(packet, header)
		base64.StdEncoding.Encode(packet[len(header):len(packet)-2], k.png[k.offset:k.offset+rawBytes])
		copy(packet[len(packet)-2:], "\x1b\\")
		k.vx.encodedUpload = k
		n, err := k.vx.tw.WriteControl(packet)
		consumed += n
		if err == nil && n != len(packet) {
			err = io.ErrShortWrite
		}
		if err != nil {
			k.err = err
			k.png = nil
			k.vx.encodedUpload = nil
			return consumed, true
		}
		k.offset += rawBytes
		if last {
			k.png = nil
			k.uploaded = true
			k.vx.encodedUpload = nil
			return consumed, true
		}
	}
	return consumed, false
}

// Err reports an upload transport failure. Failed images cannot be drawn.
func (k *EncodedKittyImage) Err() error {
	k.vx.mu.Lock()
	defer k.vx.mu.Unlock()
	return k.err
}

func (k *EncodedKittyImage) Draw(win Window) { k.DrawAtZ(win, 0) }

// DrawAtZ places the exact PNG pixels at win's origin with the requested Kitty
// z order. The caller must crop beforehand; images extending beyond a window
// or the terminal surface are not drawn. Changing z replaces the placement.
func (k *EncodedKittyImage) DrawAtZ(win Window, z int32) {
	k.vx.mu.Lock()
	defer k.vx.mu.Unlock()
	if !k.uploaded || k.destroyed || k.err != nil || win.Vx != k.vx {
		return
	}
	w, h := k.cellSizeLocked()
	if w == 0 || h == 0 {
		return
	}
	col, row := 0, 0
	for current := &win; current != nil; current = current.Parent {
		if col < 0 || row < 0 || col > current.Width-w || row > current.Height-h {
			return
		}
		col += current.Column
		row += current.Row
	}
	if col < 0 || row < 0 || col > k.vx.winSize.Cols-w || row > k.vx.winSize.Rows-h || col > 65535 || row > 65535 {
		return
	}
	pid := uint64(col)<<16 | uint64(row)
	k.vx.graphicsNext = append(k.vx.graphicsNext, &placement{
		col: col, row: row, id: k.id, w: w, h: h, z: z,
		writeTo: func(out io.Writer) {
			_, _ = fmt.Fprintf(out, "\x1b_Ga=p,i=%d,p=%d,z=%d,C=1,q=2\x1b\\", k.id, pid, z)
		},
		deleteFn: func(out io.Writer) {
			_, _ = fmt.Fprintf(out, "\x1b_Ga=d,d=i,i=%d,p=%d,q=2\x1b\\", k.id, pid)
		},
	})
}

// Destroy removes this image's placements and host asset, and releases any
// unfinished transfer. It is idempotent and does not delete unrelated images.
func (k *EncodedKittyImage) Destroy() {
	k.vx.mu.Lock()
	defer k.vx.mu.Unlock()
	if k.destroyed {
		return
	}
	k.destroyed = true
	k.png = nil
	// Kitty deletes abort any active multipart transmission, even when the
	// delete names another image. Retain that owner's PNG and restart it.
	k.vx.abortEncodedUploadLocked()
	k.vx.removeImagePlacement(k.id)
	k.vx.graphicsLast = slices.DeleteFunc(k.vx.graphicsLast, func(p *placement) bool { return p.id == k.id })
	k.vx.writeControlString(fmt.Sprintf("\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", k.id))
}

// Resize is intentionally a no-op: the PNG's exact pixels are immutable.
// Rasterize a new PNG and create a new image when a different size is needed.
func (k *EncodedKittyImage) Resize(int, int) {}

func (k *EncodedKittyImage) CellSize() (w, h int) {
	k.vx.mu.Lock()
	defer k.vx.mu.Unlock()
	return k.cellSizeLocked()
}

func (k *EncodedKittyImage) cellSizeLocked() (w, h int) {
	size := k.vx.winSize
	if size.Cols <= 0 || size.Rows <= 0 {
		return 0, 0
	}
	pixelW, pixelH := size.XPixel/size.Cols, size.YPixel/size.Rows
	if pixelW <= 0 || pixelH <= 0 {
		return 0, 0
	}
	return (k.width + pixelW - 1) / pixelW, (k.height + pixelH - 1) / pixelH
}

func (vx *Vaxis) abortEncodedUploadLocked() {
	if vx.encodedUpload != nil {
		vx.encodedUpload.offset = 0
		vx.encodedUpload = nil
	}
}
