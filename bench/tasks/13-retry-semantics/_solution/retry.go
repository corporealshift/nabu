package fixture

import (
	"context"
	"errors"
)

// ErrPermanent marks a failure that will not come good on another attempt.
var ErrPermanent = errors.New("permanent")

// Do runs op, retrying while it fails. attempts is the total number of tries,
// so Do(ctx, 3, ...) calls op at most three times.
//
// It stops early for a permanent failure and for a cancelled context, and
// returns the operation's own last error rather than one of its own: the
// caller needs to know what actually went wrong.
func Do(ctx context.Context, attempts int, op func() error) error {
	if attempts < 1 {
		attempts = 1
	}

	var last error
	for i := 0; i < attempts; i++ {
		if err := ctx.Err(); err != nil {
			if last != nil {
				return last
			}
			return err
		}

		last = op()
		switch {
		case last == nil:
			return nil
		case errors.Is(last, ErrPermanent):
			return last
		}
	}
	return last
}
