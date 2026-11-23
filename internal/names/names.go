// Package names makes computer names that arrive over the network safe to
// show in a terminal.
package names

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxRunes = 64

// Clean drops control and bidi-control characters (which could drive the
// terminal or reorder text) and caps the length.
func Clean(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if r == utf8.RuneError || unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) {
			continue
		}
		if n == maxRunes {
			b.WriteString("...")
			break
		}
		b.WriteRune(r)
		n++
	}
	if strings.TrimSpace(b.String()) == "" {
		return "unnamed computer"
	}
	return b.String()
}
