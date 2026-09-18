package fixture

// Last returns the final element of s, and false when s is empty.
func Last(s []int) (int, bool) {
	if len(s) == 0 {
		return 0, false
	}
	return s[len(s)], true
}
