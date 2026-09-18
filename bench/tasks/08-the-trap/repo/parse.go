package fixture

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseDuration reads "90m", "2h" or "45s" into seconds.
func ParseDuration(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}

	unit := s[len(s)-1]
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil {
		return 0, fmt.Errorf("bad duration %q", s)
	}

	switch unit {
	case 's':
		return n, nil
	case 'm':
		return n * 60, nil
	}
	return 0, fmt.Errorf("unknown unit %q", string(unit))
}
