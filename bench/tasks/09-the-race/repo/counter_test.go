package fixture

import (
	"sync"
	"testing"
)

const (
	writers       = 64
	perWriter     = 5000
	expectedTotal = writers * perWriter
)

func TestCounterUnderConcurrency(t *testing.T) {
	c := NewCounter()

	// Every goroutine blocks on the same channel and they are released
	// together. Without the barrier the loop is short enough that a fast
	// machine can finish one goroutine before the next is scheduled, the writes
	// never overlap, and a fixture that is supposed to be broken passes.
	start := make(chan struct{})

	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < perWriter; j++ {
				c.Add("hits")
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := c.Total("hits"); got != expectedTotal {
		t.Errorf("Total = %d, want %d", got, expectedTotal)
	}
}
