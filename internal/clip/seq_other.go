//go:build !windows && !(darwin && !ios)

package clip

import (
	"context"
	"hash/fnv"

	"golang.design/x/clipboard"
)

// changeCount has no cheap OS counter here, so it hashes the text instead.
func changeCount() int64 {
	b, _ := clipboard.Read(context.Background(), clipboard.FmtText)
	h := fnv.New64a()
	h.Write(b)
	return int64(h.Sum64())
}
