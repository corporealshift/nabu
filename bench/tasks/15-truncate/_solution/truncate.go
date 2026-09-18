package fixture

// Truncate shortens s to fit a column width characters wide, marking a cut
// with a single-character ellipsis.
//
// Counted in characters rather than bytes: these are names from everywhere,
// and a byte slice cuts an accented character in half.
func Truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}

	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	// The ellipsis occupies one of the columns it was given.
	return string(runes[:width-1]) + "…"
}
