package main

import (
	"image"
	"image/color"
	"image/draw"
	"os"
	"testing"
)

// TestIconPreview writes a contact sheet of the icons when ICON_PREVIEW is
// set to an output path; otherwise it does nothing.
func TestIconPreview(t *testing.T) {
	out := os.Getenv("ICON_PREVIEW")
	if out == "" {
		t.Skip()
	}
	sheet := image.NewNRGBA(image.Rect(0, 0, 4*80, 2*80))
	bgs := []color.NRGBA{{240, 240, 240, 255}, {30, 30, 30, 255}}
	for row, bg := range bgs {
		draw.Draw(sheet, image.Rect(0, row*80, 320, row*80+80), &image.Uniform{bg}, image.Point{}, draw.Src)
		for i, s := range []shape{macShape(false), macShape(true), winShape(false), winShape(true)} {
			img := render(64, s)
			draw.Draw(sheet, image.Rect(i*80+8, row*80+8, i*80+72, row*80+72), img, image.Point{}, draw.Over)
		}
	}
	f, _ := os.Create(out)
	defer f.Close()
	f.Write(pngBytes(sheet))
}
