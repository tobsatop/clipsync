package clip

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/png" // lets image.DecodeConfig read PNG headers
	"runtime"
	"slices"

	"golang.design/x/clipboard"
)

const maxPixels = 100_000_000 // refuse absurd images rather than run out of memory

var (
	// pngFormat is the clipboard's own PNG type: public.png on macOS (the
	// same as FmtImage there) and the registered "PNG" format on Windows,
	// which keeps transparency and comes back byte-for-byte.
	pngFormat = clipboard.Register("image/png")
	pngMagic  = []byte("\x89PNG\r\n\x1a\n")
)

// System is the real clipboard of this computer.
type System struct{}

func NewSystem() (*System, error) {
	if err := clipboard.Init(); err != nil {
		return nil, err
	}
	return &System{}, nil
}

func (*System) Seq() int64 { return changeCount() }

func (*System) Read(ctx context.Context) (Item, error) {
	// If the password check itself fails, skip this copy rather than risk
	// sending a password.
	secret, err := clipboard.Sensitive(ctx)
	if err != nil {
		return Item{}, fmt.Errorf("check for password-manager marker: %w", err)
	}
	if secret {
		return Item{}, ErrSensitive
	}
	formats, err := clipboard.Formats(ctx)
	if err != nil {
		return Item{}, err
	}
	if slices.Contains(formats, clipboard.FmtFiles) {
		return Item{}, ErrFiles
	}

	var it Item
	b, err := clipboard.Read(ctx, clipboard.FmtText)
	switch {
	case err == nil:
		it.Text = NormalizeText(b)
	case !errors.Is(err, clipboard.ErrNoData):
		return Item{}, err
	}
	if img, err := readImage(ctx); err == nil {
		it.Image = img
	} else if !errors.Is(err, clipboard.ErrNoData) {
		return Item{}, err
	}
	if it.Empty() {
		return Item{}, ErrNothing
	}
	return it, nil
}

// readImage prefers the clipboard's PNG type, which is exact, and falls back
// to whatever image the clipboard has (TIFF on macOS, a bitmap on Windows),
// converted to PNG.
func readImage(ctx context.Context) ([]byte, error) {
	b, err := clipboard.Read(ctx, pngFormat)
	if err == nil && bytes.HasPrefix(b, pngMagic) {
		return b, nil
	}
	b, err = clipboard.Read(ctx, clipboard.FmtImage)
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return nil, clipboard.ErrNoData
	}
	return b, nil
}

func (*System) Write(ctx context.Context, it Item) error {
	var items []clipboard.Option
	if len(it.Text) > 0 {
		t := it.Text
		if runtime.GOOS == "windows" {
			t = toCRLF(t)
		}
		items = append(items, clipboard.Item{Format: clipboard.FmtText, Bytes: t})
	}
	if len(it.Image) > 0 {
		if err := CheckPNG(it.Image); err != nil {
			return err
		}
		// On Windows this stores both the exact PNG and a bitmap for apps
		// that only understand bitmaps; on macOS both are public.png.
		items = append(items,
			clipboard.Item{Format: pngFormat, Bytes: it.Image},
			clipboard.Item{Format: clipboard.FmtImage, Bytes: it.Image})
	}
	if len(items) == 0 {
		return ErrNothing
	}
	_, err := clipboard.WriteAll(ctx, items...)
	return err
}

// CheckPNG accepts only a PNG of a sane size.
func CheckPNG(b []byte) error {
	if !bytes.HasPrefix(b, pngMagic) {
		return errors.New("received image is not a PNG")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("received image is damaged: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return fmt.Errorf("received image is too large (%dx%d)", cfg.Width, cfg.Height)
	}
	return nil
}

// ImageSize is the width and height of a PNG, or 0, 0 if unreadable.
func ImageSize(b []byte) (int, int) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}
