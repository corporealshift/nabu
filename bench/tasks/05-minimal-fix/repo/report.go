package fixture

import "fmt"

// Summary renders a one-line summary of a run.
func Summary(name string, passed, total int) string {
	return fmt.Sprintf("%s: %d/%s passed", name, passed, total)
}

// Detail renders the long form, and is deliberately verbose.
func Detail(name string, passed, total int) string {
	out := name + "\n"
	out += fmt.Sprintf("  passed: %d\n", passed)
	out += fmt.Sprintf("  total:  %d\n", total)
	return out
}
