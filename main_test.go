package main

import (
	"fmt"
	"testing"

	"clipsync/internal/clip"
)

var clipCheckPNG = clip.CheckPNG

func TestNormalizeCode(t *testing.T) {
	for in, want := range map[string]string{
		"482913":    "482913",
		" 482 913 ": "482913",
		"482-913":   "482913",
		"๔๘๒๙๑๓":    "482913", // Thai digits
		"ภค/ตๅ-":    "482913", // typed on a Thai keyboard without switching
		"๔๘๒-๙๑๓":   "482913",
	} {
		got, err := normalizeCode(in)
		if err != nil || got != want {
			t.Errorf("normalizeCode(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"48291", "4829134", "abcdef", "48 29 1x", ""} {
		if got, err := normalizeCode(bad); err == nil {
			t.Errorf("normalizeCode(%q) = %q, want error", bad, got)
		}
	}
}

func TestSplitArgs(t *testing.T) {
	flags, pos := splitArgs([]string{"-v", "pair", "123456", "-peer", "192.168.1.5"})
	if fmt.Sprint(flags) != "[-v -peer 192.168.1.5]" || fmt.Sprint(pos) != "[pair 123456]" {
		t.Fatalf("flags=%v pos=%v", flags, pos)
	}
}

func TestIcons(t *testing.T) {
	for _, connected := range []bool{false, true} {
		if err := clipCheckPNG(pngBytes(render(64, macShape(connected)))); err != nil {
			t.Fatal(err)
		}
		ico := icoBytes([]int{16, 32, 48}, winShape(connected))
		if len(ico) < 6 || ico[2] != 1 || ico[4] != 3 {
			t.Fatalf("bad ico header % x", ico[:6])
		}
	}
}
