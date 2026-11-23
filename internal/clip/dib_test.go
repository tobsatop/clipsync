package clip

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image/color"
	"testing"
)

// makeDIB builds a bitmap: hdrSize 40 (BITMAPINFOHEADER) or 124 (V5).
// pix(x, y) gives the colour of each pixel, top row first.
func makeDIB(hdrSize, w, h, bpp int, comp uint32, alphaMask uint32, topDown bool, pix func(x, y int) color.NRGBA) []byte {
	le := binary.LittleEndian
	stride := (w*bpp + 31) / 32 * 4
	var b bytes.Buffer
	hdr := make([]byte, hdrSize)
	le.PutUint32(hdr[0:], uint32(hdrSize))
	le.PutUint32(hdr[4:], uint32(int32(w)))
	hh := int32(h)
	if topDown {
		hh = -hh
	}
	le.PutUint32(hdr[8:], uint32(hh))
	le.PutUint16(hdr[12:], 1)
	le.PutUint16(hdr[14:], uint16(bpp))
	le.PutUint32(hdr[16:], comp)
	if hdrSize >= 56 {
		le.PutUint32(hdr[40:], 0x00FF0000)
		le.PutUint32(hdr[44:], 0x0000FF00)
		le.PutUint32(hdr[48:], 0x000000FF)
		le.PutUint32(hdr[52:], alphaMask)
	}
	b.Write(hdr)
	if hdrSize == 40 && comp == 3 {
		m := make([]byte, 12)
		le.PutUint32(m[0:], 0x00FF0000)
		le.PutUint32(m[4:], 0x0000FF00)
		le.PutUint32(m[8:], 0x000000FF)
		b.Write(m)
	}
	for row := 0; row < h; row++ {
		y := row
		if !topDown {
			y = h - 1 - row
		}
		line := make([]byte, stride)
		for x := 0; x < w; x++ {
			c := pix(x, y)
			if bpp == 24 {
				line[x*3], line[x*3+1], line[x*3+2] = c.B, c.G, c.R
			} else {
				line[x*4], line[x*4+1], line[x*4+2], line[x*4+3] = c.B, c.G, c.R, c.A
			}
		}
		b.Write(line)
	}
	return b.Bytes()
}

// gradient differs in every pixel, so flipped rows or swapped channels show.
func gradient(alpha uint8) func(x, y int) color.NRGBA {
	return func(x, y int) color.NRGBA {
		return color.NRGBA{uint8(x * 40), uint8(y * 60), uint8(200 - x*10), alpha}
	}
}

func checkPixels(t *testing.T, name string, b []byte, w, h int, want func(x, y int) color.NRGBA) {
	t.Helper()
	img, err := dibToImage(b)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if got := img.Bounds().Size(); got.X != w || got.Y != h {
		t.Fatalf("%s: size %v", name, got)
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			got := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			if got != want(x, y) {
				t.Fatalf("%s: pixel (%d,%d) = %v, want %v", name, x, y, got, want(x, y))
			}
		}
	}
}

func TestDIBLayouts(t *testing.T) {
	opaque := gradient(255)
	zeroA := gradient(0)
	half := gradient(128)

	// Alt+PrintScreen style: 24-bit, bottom-up.
	checkPixels(t, "24-bit bottom-up", makeDIB(40, 5, 3, 24, 0, 0, false, opaque), 5, 3, opaque)
	// 32-bit BI_RGB: the 4th byte is unused even if zero, so the result is opaque.
	checkPixels(t, "32-bit BI_RGB", makeDIB(40, 5, 3, 32, 0, 0, false, zeroA), 5, 3, opaque)
	// Top-down rows (negative height) must not come out empty or flipped.
	checkPixels(t, "32-bit top-down", makeDIB(40, 5, 3, 32, 0, 0, true, zeroA), 5, 3, opaque)
	// BI_BITFIELDS with masks after a plain header.
	checkPixels(t, "bitfields after header", makeDIB(40, 4, 2, 32, 3, 0, false, zeroA), 4, 2, opaque)
	// V5 with an alpha mask and real alpha keeps transparency.
	checkPixels(t, "V5 with alpha", makeDIB(124, 4, 4, 32, 3, 0xFF000000, false, half), 4, 4, half)
	// V5 with BI_RGB and an alpha mask (as golang.design writes) keeps it too.
	checkPixels(t, "V5 BI_RGB with alpha", makeDIB(124, 4, 4, 32, 0, 0xFF000000, false, half), 4, 4, half)
	// V5 with an alpha mask but all-zero alpha is opaque, not invisible.
	checkPixels(t, "V5 unused alpha", makeDIB(124, 4, 4, 32, 3, 0xFF000000, false, zeroA), 4, 4, opaque)
	// Odd width exercises row padding.
	checkPixels(t, "24-bit odd width", makeDIB(40, 3, 2, 24, 0, 0, false, opaque), 3, 2, opaque)
}

func TestDIBRejects(t *testing.T) {
	good := makeDIB(40, 4, 4, 24, 0, 0, false, gradient(255))
	if _, err := dibToImage(good[:len(good)-1]); err == nil {
		t.Error("truncated bitmap accepted")
	}
	if _, err := dibToImage(good[:30]); err == nil {
		t.Error("short header accepted")
	}
	eight := makeDIB(40, 4, 4, 24, 0, 0, false, gradient(255))
	binary.LittleEndian.PutUint16(eight[14:], 8)
	if _, err := dibToImage(eight); !errors.Is(err, errUnsupportedDIB) {
		t.Errorf("8-bit: %v", err)
	}
	huge := makeDIB(40, 1, 1, 24, 0, 0, false, gradient(255))
	binary.LittleEndian.PutUint32(huge[4:], 100000)
	binary.LittleEndian.PutUint32(huge[8:], 100000)
	if _, err := dibToImage(huge); err == nil {
		t.Error("10-gigapixel bitmap accepted")
	}
}

func TestEncodedDIBIsValidPNG(t *testing.T) {
	img, err := dibToImage(makeDIB(124, 30, 20, 32, 3, 0xFF000000, true, gradient(200)))
	if err != nil {
		t.Fatal(err)
	}
	b, err := encodePNG(img)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckPNG(b); err != nil {
		t.Fatal(err)
	}
	if w, h := ImageSize(b); w != 30 || h != 20 {
		t.Fatalf("size %dx%d", w, h)
	}
}
