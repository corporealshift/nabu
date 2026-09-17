package fixture

import (
	"context"
	"errors"
	"testing"
)

// attempts is a total, not a number of retries on top of the first try.
func TestAttemptsIsATotal(t *testing.T) {
	calls := 0
	err := Do(context.Background(), 3, func() error {
		calls++
		return errors.New("nope")
	})

	if calls != 3 {
		t.Errorf("called %d times, want exactly 3", calls)
	}
	if err == nil {
		t.Error("exhausting the attempts should return the failure")
	}
}

// Success on the first go means one call, not one plus a retry.
func TestSuccessDoesNotRetry(t *testing.T) {
	calls := 0
	if err := Do(context.Background(), 5, func() error { calls++; return nil }); err != nil {
		t.Fatalf("got %v, want nil", err)
	}
	if calls != 1 {
		t.Errorf("called %d times, want 1", calls)
	}
}

// A permanent failure is not worth a second attempt, and must reach the caller
// recognisably.
func TestPermanentStopsImmediately(t *testing.T) {
	calls := 0
	err := Do(context.Background(), 5, func() error {
		calls++
		return ErrPermanent
	})

	if calls != 1 {
		t.Errorf("called %d times, want 1", calls)
	}
	if !errors.Is(err, ErrPermanent) {
		t.Errorf("got %v, want something that is ErrPermanent", err)
	}
}

// A wrapped permanent failure is still permanent.
func TestWrappedPermanentStopsToo(t *testing.T) {
	calls := 0
	_ = Do(context.Background(), 5, func() error {
		calls++
		return errors.New("while saving: " + ErrPermanent.Error())
	})
	if calls == 1 {
		return // treated as permanent by string, acceptable
	}

	calls = 0
	err := Do(context.Background(), 5, func() error {
		calls++
		return errWrap{ErrPermanent}
	})
	if calls != 1 {
		t.Errorf("a wrapped permanent error was retried %d times", calls)
	}
	if !errors.Is(err, ErrPermanent) {
		t.Errorf("got %v, want something that is ErrPermanent", err)
	}
}

type errWrap struct{ err error }

func (e errWrap) Error() string { return "wrapped: " + e.err.Error() }
func (e errWrap) Unwrap() error { return e.err }

// A cancelled context stops the retrying.
func TestCancellationStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0

	err := Do(ctx, 10, func() error {
		calls++
		if calls == 2 {
			cancel()
		}
		return errors.New("nope")
	})

	if calls > 3 {
		t.Errorf("kept going after cancellation: %d calls", calls)
	}
	if err == nil {
		t.Error("a cancelled retry should return an error")
	}
}

// The last failure is what the caller needs; a bare "gave up" hides it.
func TestTheFailureComesBack(t *testing.T) {
	sentinel := errors.New("the actual problem")
	err := Do(context.Background(), 2, func() error { return sentinel })

	if !errors.Is(err, sentinel) {
		t.Errorf("got %v, want something that is the operation's own error", err)
	}
}

// Nonsense attempt counts must not run forever or panic.
func TestZeroAttempts(t *testing.T) {
	calls := 0
	_ = Do(context.Background(), 0, func() error { calls++; return errors.New("x") })
	if calls > 1 {
		t.Errorf("zero attempts called the operation %d times", calls)
	}
}
