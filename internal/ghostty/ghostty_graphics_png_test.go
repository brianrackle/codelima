//go:build cgo && (darwin || linux)

package ghostty

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestGhosttyPNGDecodePreservesStraightAlpha(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	source.SetNRGBA(0, 0, color.NRGBA{R: 201, G: 102, B: 53, A: 128})
	source.SetNRGBA(1, 0, color.NRGBA{R: 19, G: 37, B: 55, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeGhosttyPNG(encoded.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	dst := make([]byte, 8)
	writeGhosttyRGBA(dst, decoded)
	if !bytes.Equal(dst, source.Pix) {
		t.Fatalf("RGBA=%v, want nonpremultiplied %v", dst, source.Pix)
	}
}

func TestGhosttyPNGRejectsMalformedAndOversized(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	oversized := bytes.Clone(encoded.Bytes())
	binary.BigEndian.PutUint32(oversized[16:20], 2049)
	binary.BigEndian.PutUint32(oversized[20:24], 2049)
	binary.BigEndian.PutUint32(oversized[29:33], crc32.ChecksumIEEE(oversized[12:29]))
	for name, data := range map[string][]byte{
		"empty": nil, "invalid": []byte("not PNG"),
		"truncated": encoded.Bytes()[:40], "dimensions": oversized,
		"input": make([]byte, ghosttyPNGMaxBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeGhosttyPNG(data); err == nil {
				t.Fatal("unsafe PNG accepted")
			}
		})
	}
}
