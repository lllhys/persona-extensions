package telegram

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

// A long error line is clipped to 200 runes and stays valid UTF-8: the byte cut must not land
// in the middle of a multibyte rune.
func TestExplainClipsMultibyteLine(t *testing.T) {
	// "中" is three bytes: 66 runes are 198 bytes, so byte 200 lands inside the 67th rune.
	line := strings.Repeat("中", 210)
	got := explain(errors.New(line + "\nsecond line"))
	if !utf8.ValidString(got) {
		t.Fatalf("explain clipped to invalid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("explain %q is not marked with the ellipsis", got)
	}
	if strings.Contains(got, "\n") {
		t.Fatalf("explain %q kept more than the first line", got)
	}
	if want := strings.Repeat("中", 200) + "…"; got != want {
		t.Fatalf("explain = %q, want %q", got, want)
	}
}

// A long ASCII-only line is cut byte-for-byte as before: 200 bytes and the ellipsis.
func TestExplainClipsASCIILine(t *testing.T) {
	got := explain(errors.New(strings.Repeat("a", 201)))
	if want := strings.Repeat("a", 200) + "…"; got != want {
		t.Fatalf("explain = %q, want %q", got, want)
	}
}

// Only the first line of an error is shown, even without truncation.
func TestExplainCutsAtNewline(t *testing.T) {
	if got := explain(errors.New("first line\nsecond line")); got != "first line" {
		t.Fatalf("explain = %q, want %q", got, "first line")
	}
}
