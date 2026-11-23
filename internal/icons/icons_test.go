package icons

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"testing"
)

func TestIconsEncode(t *testing.T) {
	for _, s := range []Shape{MacTray(false), MacTray(true), WinTray(false), WinTray(true), App(0.1)} {
		if _, err := png.Decode(bytes.NewReader(PNG(Render(64, s)))); err != nil {
			t.Fatal(err)
		}
	}
	ico := ICO([]int{16, 32, 256}, App(0))
	if len(ico) < 6 || ico[2] != 1 || ico[4] != 3 || ico[6+2*16] != 0 /* 256 is stored as 0 */ {
		t.Fatalf("bad ico header % x", ico[:6])
	}
}

// TestPreview writes a contact sheet when ICON_PREVIEW names a file.
func TestPreview(t *testing.T) {
	out := os.Getenv("ICON_PREVIEW")
	if out == "" {
		t.Skip("set ICON_PREVIEW=path.png to write a preview")
	}
	const cell = 140
	sheet := image.NewNRGBA(image.Rect(0, 0, 5*cell, 2*cell))
	for row, bg := range []color.NRGBA{{243, 244, 246, 255}, {32, 32, 32, 255}} {
		draw.Draw(sheet, image.Rect(0, row*cell, 5*cell, (row+1)*cell), &image.Uniform{bg}, image.Point{}, draw.Src)
		put := func(i, size int, s Shape) {
			off := (cell - size) / 2
			draw.Draw(sheet, image.Rect(i*cell+off, row*cell+off, i*cell+off+size, row*cell+off+size), Render(size, s), image.Point{}, draw.Over)
		}
		put(0, 128, App(0))
		put(1, 48, App(0))
		put(2, 24, App(0))
		put(3, 32, WinTray(true))
		put(4, 32, WinTray(false))
	}
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.Write(PNG(sheet))
}
