package clip

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/png"
)

// errUnsupportedDIB means a bitmap layout clipsync does not convert (8-bit
// palette, RLE, unusual channel masks).
var errUnsupportedDIB = errors.New("unsupported bitmap format")

// dibToImage converts a Windows device-independent bitmap, as found in
// CF_DIB or CF_DIBV5 (a BITMAPINFOHEADER, V4 or V5 header followed by the
// pixels), into an image. It handles 24- and 32-bit pixels, bottom-up and
// top-down rows, and treats the fourth byte as alpha only when the header
// says it is and at least one pixel uses it (many apps leave it at zero).
func dibToImage(b []byte) (image.Image, error) {
	if len(b) < 40 {
		return nil, errors.New("bitmap header too short")
	}
	le := binary.LittleEndian
	hdr := int(le.Uint32(b[0:]))
	w := int(int32(le.Uint32(b[4:])))
	h := int(int32(le.Uint32(b[8:])))
	bpp := int(le.Uint16(b[14:]))
	comp := le.Uint32(b[16:])
	clrUsed := int(le.Uint32(b[32:]))
	if hdr < 40 || hdr > len(b) {
		return nil, errors.New("bad bitmap header")
	}
	topDown := h < 0
	if topDown {
		h = -h
	}
	if w <= 0 || h <= 0 {
		return nil, errors.New("bad bitmap size")
	}
	if int64(w)*int64(h) > maxPixels {
		return nil, fmt.Errorf("image too large (%dx%d)", w, h)
	}
	if bpp != 24 && bpp != 32 {
		return nil, fmt.Errorf("%w (%d bits per pixel)", errUnsupportedDIB, bpp)
	}

	const biRGB, biBitfields = 0, 3
	off := hdr
	var alphaMask uint32
	switch comp {
	case biRGB:
	case biBitfields:
		if bpp != 32 {
			return nil, errUnsupportedDIB
		}
		if len(b) < 52 {
			return nil, errors.New("bitmap masks missing")
		}
		masks := b[40:]
		if hdr < 52 { // masks follow a plain BITMAPINFOHEADER
			off = 52
		}
		r, g, bl := le.Uint32(masks[0:]), le.Uint32(masks[4:]), le.Uint32(masks[8:])
		if r != 0x00FF0000 || g != 0x0000FF00 || bl != 0x000000FF {
			return nil, fmt.Errorf("%w (channel masks)", errUnsupportedDIB)
		}
		if hdr >= 56 {
			alphaMask = le.Uint32(b[52:])
		}
	default:
		return nil, fmt.Errorf("%w (compression %d)", errUnsupportedDIB, comp)
	}
	if clrUsed < 0 || clrUsed > 256 {
		return nil, errors.New("bad bitmap color table")
	}
	off += clrUsed * 4

	stride := (w*bpp + 31) / 32 * 4
	if int64(off)+int64(stride)*int64(h) > int64(len(b)) {
		return nil, errors.New("bitmap data is truncated")
	}
	useAlpha := bpp == 32 && alphaMask == 0xFF000000
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	sawAlpha := false
	for y := 0; y < h; y++ {
		src := y
		if !topDown {
			src = h - 1 - y
		}
		row := b[off+src*stride:]
		dst := img.Pix[y*img.Stride:]
		for x := 0; x < w; x++ {
			var p []byte
			if bpp == 24 {
				p = row[x*3 : x*3+3]
			} else {
				p = row[x*4 : x*4+4]
			}
			d := dst[x*4 : x*4+4]
			d[0], d[1], d[2], d[3] = p[2], p[1], p[0], 255
			if useAlpha {
				d[3] = p[3]
				if p[3] != 0 {
					sawAlpha = true
				}
			}
		}
	}
	if useAlpha && !sawAlpha { // alpha channel present but unused: opaque
		for i := 3; i < len(img.Pix); i += 4 {
			img.Pix[i] = 255
		}
	}
	return img, nil
}

// encodePNG favours speed: these images are sent once and pasted once.
func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
