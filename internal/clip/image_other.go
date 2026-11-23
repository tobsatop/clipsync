//go:build !windows && !(darwin && !ios)

package clip

import (
	"context"

	"golang.design/x/clipboard"
)

func readImageFallback(ctx context.Context) ([]byte, error) {
	return clipboard.Read(ctx, clipboard.FmtImage)
}
