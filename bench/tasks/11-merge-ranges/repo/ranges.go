package fixture

// Range is a half-open booking: Start is included, End is not.
type Range struct {
	Start int
	End   int
}

// Merge collapses overlapping bookings into single ranges.
func Merge(in []Range) []Range {
	panic("not implemented")
}
