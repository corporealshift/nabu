package fixture

import (
	"testing"
	"unicode/utf8"
)

func TestTruncateContract(t *testing.T) {
	tests := []struct {
		name string
		in   string
		w    int
		want string
	}{
		{"fits already", "abc", 5, "abc"},
		{"exactly fits", "abcde", 5, "abcde"},
		{"cuts with an ellipsis", "abcdefgh", 5, "abcd…"},
		{"empty stays empty", "", 5, ""},
		// The ellipsis is one character, and it has to fit inside the width.
		{"width of one", "abcdef", 1, "…"},
		// Characters, not bytes: é is two bytes and one character.
		{"accents count once", "ééééé", 5, "ééééé"},
		{"accents cut by character", "ééééééé", 3, "éé…"},
		{"mixed", "café society", 6, "café …"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Truncate(tt.in, tt.w); got != tt.want {
				t.Errorf("Truncate(%q, %d) = %q, want %q", tt.in, tt.w, got, tt.want)
			}
		})
	}
}

// Whatever it returns must fit the column it was given.
func TestTruncateNeverExceedsTheWidth(t *testing.T) {
	for _, in := range []string{"abcdefgh", "ééééééé", "café society", "日本語のテキスト"} {
		for w := 1; w <= 10; w++ {
			got := Truncate(in, w)
			if n := utf8.RuneCountInString(got); n > w {
				t.Errorf("Truncate(%q, %d) = %q, which is %d characters wide", in, w, got, n)
			}
		}
	}
}

// A cut string must never end mid-character.
func TestTruncateKeepsCharactersWhole(t *testing.T) {
	for w := 1; w <= 8; w++ {
		got := Truncate("ééééééé", w)
		if !utf8.ValidString(got) {
			t.Errorf("Truncate(..., %d) = %q, which is not valid UTF-8", w, got)
		}
	}
}
