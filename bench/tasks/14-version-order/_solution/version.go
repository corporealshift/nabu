package fixture

import (
	"strconv"
	"strings"
)

// Less reports whether version a comes before version b.
//
// Two things the obvious implementation gets wrong: the numbers are numbers,
// so 1.10.0 is after 1.9.0 however the strings compare; and a pre-release
// comes *before* the release it leads to, so 1.0.0-rc1 is before 1.0.0.
func Less(a, b string) bool {
	anum, apre := parseVersion(a)
	bnum, bpre := parseVersion(b)

	for i := 0; i < 3; i++ {
		if anum[i] != bnum[i] {
			return anum[i] < bnum[i]
		}
	}

	switch {
	case apre == bpre:
		return false
	case apre == "":
		return false // a is the release, b leads to it
	case bpre == "":
		return true
	default:
		return apre < bpre
	}
}

func parseVersion(v string) ([3]int, string) {
	var pre string
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v, pre = v[:i], v[i+1:]
	}

	var nums [3]int
	for i, part := range strings.SplitN(v, ".", 3) {
		if i > 2 {
			break
		}
		nums[i], _ = strconv.Atoi(part)
	}
	return nums, pre
}
