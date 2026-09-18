package fixture

import (
	"sync"
	"testing"
)

func TestCounterUnderConcurrency(t *testing.T) {
	c := NewCounter()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				c.Add("hits")
			}
		}()
	}
	wg.Wait()

	if got := c.Total("hits"); got != 5000 {
		t.Errorf("Total = %d, want 5000", got)
	}
}
