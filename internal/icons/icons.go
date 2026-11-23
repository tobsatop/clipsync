// Package icons draws clipsync's icons in code, so there are no image
// files to keep in sync.
package icons

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

type Shape func(x, y float64) color.NRGBA

// Render samples shape 4x4 times per pixel for smooth edges.
func Render(size int, f Shape) *image.NRGBA {
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

func MacTray(connected bool) Shape {
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

func WinTray(connected bool) Shape {
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

func PNG(img image.Image) []byte {
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

// ICO packs PNG images into a Windows .ico file.
func ICO(sizes []int, f Shape) []byte {
	var imgs [][]byte
	for _, s := range sizes {
		imgs = append(imgs, PNG(Render(s, f)))
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

// App is the application icon: a blue tile with a white clipboard and two
// arrows going round, for "kept in sync". margin is the empty border as a
// fraction of the size (macOS icons leave about 10%).
func App(margin float64) Shape {
	top := color.NRGBA{0x5B, 0x9B, 0xFF, 255}
	bottom := color.NRGBA{0x1D, 0x4E, 0xD8, 255}
	white := color.NRGBA{255, 255, 255, 255}
	clipFill := color.NRGBA{0xDB, 0xEA, 0xFE, 255}
	navy := color.NRGBA{0x1E, 0x3A, 0x8A, 255}
	arrow := color.NRGBA{0x25, 0x63, 0xEB, 255}
	lo, hi := margin, 1-margin
	return func(x, y float64) color.NRGBA {
		if !inRoundRect(x, y, lo, lo, hi, hi, 0.22*(hi-lo)) {
			return transparent
		}
		// Back to 0..1 inside the tile.
		tx, ty := (x-lo)/(hi-lo), (y-lo)/(hi-lo)
		c := mix(top, bottom, ty)
		bx0, by0, bx1, by1 := 0.22, 0.20, 0.78, 0.88
		if inRoundRect(tx, ty-0.025, bx0, by0, bx1, by1, 0.08) && !inRoundRect(tx, ty, bx0, by0, bx1, by1, 0.08) {
			c = mix(c, color.NRGBA{A: 255}, 0.25) // soft shadow under the board
		}
		if inRoundRect(tx, ty, bx0, by0, bx1, by1, 0.08) {
			c = white
			if inSyncArrows(tx, ty, 0.5, 0.57) {
				c = arrow
			}
		}
		if inRoundRect(tx, ty, 0.37, 0.12, 0.63, 0.27, 0.05) {
			c = clipFill
			if inRoundRect(tx, ty, 0.45, 0.155, 0.55, 0.205, 0.025) {
				c = navy
			}
		}
		return c
	}
}

func mix(a, b color.NRGBA, t float64) color.NRGBA {
	t = math.Max(0, math.Min(1, t))
	m := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t) }
	return color.NRGBA{m(a.R, b.R), m(a.G, b.G), m(a.B, b.B), m(a.A, b.A)}
}

// inSyncArrows draws two arcs around (cx, cy), each ending in an arrowhead,
// both turning clockwise.
func inSyncArrows(x, y, cx, cy float64) bool {
	const r0, r1 = 0.12, 0.175
	rm := (r0 + r1) / 2
	dx, dy := x-cx, y-cy
	d := math.Hypot(dx, dy)
	a := math.Atan2(dy, dx) * 180 / math.Pi // screen angle, clockwise from +x
	inArc := func(from, to float64) bool { return d >= r0 && d <= r1 && a >= from && a <= to }
	if inArc(-165, -40) || inArc(15, 140) {
		return true
	}
	head := func(end float64) bool {
		rad := func(deg float64) float64 { return deg * math.Pi / 180 }
		pt := func(ang, r float64) (float64, float64) { return cx + r*math.Cos(rad(ang)), cy + r*math.Sin(rad(ang)) }
		ax, ay := pt(end, rm-0.07)
		bx, by := pt(end, rm+0.07)
		tx, ty := pt(end+32, rm)
		return inTriangle(x, y, ax, ay, bx, by, tx, ty)
	}
	return head(-40) || head(140)
}

func inTriangle(px, py, ax, ay, bx, by, cx, cy float64) bool {
	s := func(x1, y1, x2, y2, x3, y3 float64) float64 { return (x1-x3)*(y2-y3) - (x2-x3)*(y1-y3) }
	d1, d2, d3 := s(px, py, ax, ay, bx, by), s(px, py, bx, by, cx, cy), s(px, py, cx, cy, ax, ay)
	neg := d1 < 0 || d2 < 0 || d3 < 0
	pos := d1 > 0 || d2 > 0 || d3 > 0
	return !(neg && pos)
}
