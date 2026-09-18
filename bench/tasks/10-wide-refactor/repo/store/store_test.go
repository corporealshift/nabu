package store

import (
	"errors"
	"testing"
)

// Every Fetch must report the new error. The old one is on its way out.
func TestEveryFetchUsesTheNewError(t *testing.T) {
	fetches := []func(string) (string, error){
		Fetch1, Fetch2, Fetch3, Fetch4, Fetch5, Fetch6, Fetch7, Fetch8,
	}
	for i, fetch := range fetches {
		if _, err := fetch(""); !errors.Is(err, ErrNoID) {
			t.Errorf("Fetch%d returned %v, want ErrNoID", i+1, err)
		}
	}
}
