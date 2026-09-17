package format

import (
	"fixture/internal/pad"
	"strings"
)

// Table renders rows as aligned columns.
func Table(rows [][]string, widths []int) string {
	var b strings.Builder
	for _, row := range rows {
		for i, cell := range row {
			b.WriteString(pad.Right(cell, widths[i]))
		}
		b.WriteString("\n")
	}
	return b.String()
}
