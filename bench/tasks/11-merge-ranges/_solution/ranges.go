package fixture

import "sort"

// Range is a half-open booking: Start is included, End is not.
type Range struct {
	Start int
	End   int
}

// Merge collapses overlapping bookings into single ranges. Touching ranges are
// merged too: the gap between [1,3) and [3,5) is empty, and leaving it is a
// slot nobody can book.
func Merge(in []Range) []Range {
	if len(in) == 0 {
		return nil
	}

	sorted := append([]Range(nil), in...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Start != sorted[j].Start {
			return sorted[i].Start < sorted[j].Start
		}
		return sorted[i].End < sorted[j].End
	})

	out := []Range{sorted[0]}
	for _, r := range sorted[1:] {
		last := &out[len(out)-1]
		if r.Start <= last.End {
			if r.End > last.End {
				last.End = r.End
			}
			continue
		}
		out = append(out, r)
	}
	return out
}
