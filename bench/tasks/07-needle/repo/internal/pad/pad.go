package pad

import "strings"

// Right pads s on the right so the result is width characters wide.
func Right(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s)-1)
}
