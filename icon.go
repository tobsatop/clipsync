package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
)

// The tray icon is a clipboard, drawn in code: solid when connected,
// outlined (Mac) or grey (Windows) when not. The Mac menu bar gets
// monochrome "template" images that macOS tints for light and dark mode;
// Windows gets coloured icons that read on both light and dark taskbars.

type shape func(x, y float64) color.NRGBA

// render samples shape 4x4 times per pixel for smooth edges.
func render(size int, f shape) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	const ss = 4
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var r, g, b, a float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					c := f((float64(px)+(float64(sx)+0.5)/ss)/float64(size), (float64(py)+(float64(sy)+0.5)/ss)/float64(size))
					ca := float64(c.A) / 255
					r += float64(c.R) * ca
					g += float64(c.G) * ca
					b += float64(c.B) * ca
					a += ca
				}
			}
			if a == 0 {
				continue
			}
			img.SetNRGBA(px, py, color.NRGBA{
				R: uint8(r / a), G: uint8(g / a), B: uint8(b / a),
				A: uint8(math.Round(a / (ss * ss) * 255)),
			})
		}
	}
	return img
}

func inRoundRect(x, y, x0, y0, x1, y1, r float64) bool {
	if x < x0 || x > x1 || y < y0 || y > y1 {
		return false
	}
	cx := math.Max(x0+r, math.Min(x, x1-r))
	cy := math.Max(y0+r, math.Min(y, y1-r))
	return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= r*r
}

func inBoard(x, y float64) bool { return inRoundRect(x, y, 0.16, 0.12, 0.84, 0.95, 0.12) }
func inBoardInner(x, y float64) bool {
	return inRoundRect(x, y, 0.25, 0.21, 0.75, 0.86, 0.05)
}
func inClip(x, y float64) bool { return inRoundRect(x, y, 0.33, 0.03, 0.67, 0.24, 0.06) }
func inLine(x, y float64) bool {
	for _, ly := range []float64{0.42, 0.58, 0.74} {
		if x >= 0.30 && x <= 0.70 && y >= ly-0.045 && y <= ly+0.045 {
			return true
		}
	}
	return false
}

var transparent = color.NRGBA{}

func macShape(connected bool) shape {
	black := color.NRGBA{A: 255}
	return func(x, y float64) color.NRGBA {
		switch {
		case inClip(x, y):
			return black
		case connected && inBoard(x, y) && !inLine(x, y):
			return black // solid board with the lines cut out
		case !connected && inBoard(x, y) && !inBoardInner(x, y):
			return black // outline only
		}
		return transparent
	}
}

func winShape(connected bool) shape {
	fill, clip := color.NRGBA{0x25, 0x63, 0xEB, 255}, color.NRGBA{0x1E, 0x3A, 0x8A, 255}
	if !connected {
		fill, clip = color.NRGBA{0x9C, 0xA3, 0xAF, 255}, color.NRGBA{0x4B, 0x55, 0x63, 255}
	}
	white := color.NRGBA{255, 255, 255, 255}
	return func(x, y float64) color.NRGBA {
		switch {
		case inClip(x, y):
			return clip
		case inBoard(x, y) && inLine(x, y):
			return white
		case inBoard(x, y):
			return fill
		}
		return transparent
	}
}

func pngBytes(img image.Image) []byte {
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

// icoBytes packs PNG images into a Windows .ico file.
func icoBytes(sizes []int, f shape) []byte {
	var imgs [][]byte
	for _, s := range sizes {
		imgs = append(imgs, pngBytes(render(s, f)))
	}
	var buf bytes.Buffer
	le := binary.LittleEndian
	binary.Write(&buf, le, [3]uint16{0, 1, uint16(len(imgs))})
	offset := 6 + 16*len(imgs)
	for i, b := range imgs {
		dim := uint8(sizes[i])
		if sizes[i] >= 256 {
			dim = 0
		}
		buf.Write([]byte{dim, dim, 0, 0})
		binary.Write(&buf, le, [2]uint16{1, 32})
		binary.Write(&buf, le, [2]uint32{uint32(len(b)), uint32(offset)})
		offset += len(b)
	}
	for _, b := range imgs {
		buf.Write(b)
	}
	return buf.Bytes()
}
