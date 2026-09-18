package fixture

// Counter totals what several goroutines report.
type Counter struct {
	counts map[string]int
}

func NewCounter() *Counter {
	return &Counter{counts: map[string]int{}}
}

// Add records one occurrence of name.
func (c *Counter) Add(name string) {
	c.counts[name]++
}

// Total is how many times name was seen.
func (c *Counter) Total(name string) int {
	return c.counts[name]
}
