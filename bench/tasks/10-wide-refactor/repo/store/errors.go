package store

import "errors"

// ErrMissing is returned when an id is empty.
//
// It is being replaced by ErrNoID, which says what is actually wrong rather
// than what the caller wanted. Every Fetch must return ErrNoID instead.
var ErrMissing = errors.New("missing")

// ErrNoID is the replacement.
var ErrNoID = errors.New("no id given")
