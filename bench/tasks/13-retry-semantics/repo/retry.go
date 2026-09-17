package fixture

import (
	"context"
	"errors"
)

// ErrPermanent marks a failure that will not come good on another attempt.
var ErrPermanent = errors.New("permanent")

// Do runs op, retrying while it fails. attempts is the total number of tries.
func Do(ctx context.Context, attempts int, op func() error) error {
	panic("not implemented")
}
