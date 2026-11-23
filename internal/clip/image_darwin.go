//go:build darwin && !ios

package clip

import (
	"bytes"
	"context"
	"fmt"

	"golang.design/x/clipboard"
	"golang.org/x/image/tiff"
)

// tiffFormat is public.tiff, how many Mac apps (Preview, Photos) copy
// images. Reading it raw lets clipsync check the size before decoding.
var tiffFormat = clipboard.Register("image/tiff")

func readImageFallback(ctx context.Context) ([]byte, error) {
	raw, err := clipboard.Read(ctx, tiffFormat)
	if err != nil {
		return nil, err
	}
	cfg, err := tiff.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("copied image is damaged: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return nil, fmt.Errorf("copied image is too large (%dx%d)", cfg.Width, cfg.Height)
	}
	img, err := tiff.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("copied image is damaged: %w", err)
	}
	return encodePNG(img)
}
