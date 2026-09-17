package fixture

import (
	"context"
	"errors"
	"testing"
)

func TestDoRetriesUntilItWorks(t *testing.T) {
	calls := 0
	err := Do(context.Background(), 5, func() error {
		calls++
		if calls < 3 {
			return errors.New("not yet")
		}
		return nil
	})

	if err != nil {
		t.Fatalf("got %v, want nil", err)
	}
	if calls != 3 {
		t.Errorf("called %d times, want 3", calls)
	}
}
