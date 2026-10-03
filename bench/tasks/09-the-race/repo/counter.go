package fixture

import "runtime"

// Counter totals what several goroutines report.
type Counter struct {
	counts map[string]int
}

func NewCounter() *Counter {
	return &Counter{counts: map[string]int{}}
}

// Add records one occurrence of name.
func (c *Counter) Add(name string) {
	n := c.counts[name]
	// Yielding between the read and the write makes the lost update happen
	// on one core too. Without it, a CI runner that gave the test one core
	// ran the writers one after another and this broken fixture passed.
	runtime.Gosched()
	c.counts[name] = n + 1
}

// Total is how many times name was seen.
func (c *Counter) Total(name string) int {
	return c.counts[name]
}
