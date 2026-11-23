package names

import "testing"

func TestClean(t *testing.T) {
	for in, want := range map[string]string{
		"MacBook-Air":               "MacBook-Air",
		"\x1b[2J\x1b]0;pwned\x07PC": "[2J]0;pwnedPC",
		"evil‮gnp.exe":              "evilgnp.exe",
		"":                          "unnamed computer",
		"\n\t":                      "unnamed computer",
		"คอมที่บ้าน":                "คอมที่บ้าน",
	} {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
	long := Clean(string(make([]rune, 0)) + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if len([]rune(long)) != maxRunes+3 {
		t.Errorf("long name not capped: %d runes", len([]rune(long)))
	}
}
