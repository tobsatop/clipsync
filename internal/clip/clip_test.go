package clip

import "testing"

func TestNormalizeTextIsIdempotent(t *testing.T) {
	for in, want := range map[string]string{
		"a\r\nb":        "a\nb",
		"a\r\r\nb":      "a\nb",
		"a\r\r\r\nb\n":  "a\nb\n",
		"lone\rcr":      "lone\rcr",
		"nul\x00inside": "nulinside",
		"ไทย\r\nok":     "ไทย\nok",
	} {
		got := string(NormalizeText([]byte(in)))
		if got != want {
			t.Errorf("NormalizeText(%q) = %q, want %q", in, got, want)
		}
		if again := string(NormalizeText([]byte(got))); again != got {
			t.Errorf("not idempotent on %q", in)
		}
		// What Windows writes and reads back must hash the same.
		if back := string(NormalizeText(toCRLF([]byte(got)))); back != got {
			t.Errorf("CRLF round trip of %q gave %q", got, back)
		}
	}
}
