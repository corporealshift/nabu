package fixture

import (
	"reflect"
	"testing"
)

// What a booking system's callers expect, spelled out.
func TestMergeContract(t *testing.T) {
	tests := []struct {
		name string
		in   []Range
		want []Range
	}{
		{"nothing in, nothing out", nil, nil},
		{"one stays one", []Range{{1, 3}}, []Range{{1, 3}}},
		{"overlapping merge", []Range{{1, 5}, {3, 8}}, []Range{{1, 8}}},
		// Half-open: [1,3) and [3,5) do not overlap, but a booking system that
		// leaves them apart has an empty gap nobody can book.
		{"touching merge", []Range{{1, 3}, {3, 5}}, []Range{{1, 5}}},
		{"a gap is kept", []Range{{1, 3}, {4, 6}}, []Range{{1, 3}, {4, 6}}},
		// Input order is not the caller's problem.
		{"unsorted input", []Range{{5, 8}, {1, 3}, {2, 6}}, []Range{{1, 8}}},
		{"fully contained", []Range{{1, 10}, {3, 4}}, []Range{{1, 10}}},
		{"identical", []Range{{2, 4}, {2, 4}}, []Range{{2, 4}}},
		{"chain", []Range{{1, 2}, {2, 3}, {3, 4}}, []Range{{1, 4}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Merge(tt.in)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Merge(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// Merging must not scribble on what it was handed.
func TestMergeDoesNotMutateInput(t *testing.T) {
	in := []Range{{5, 8}, {1, 3}, {2, 6}}
	keep := append([]Range(nil), in...)

	Merge(in)

	if !reflect.DeepEqual(in, keep) {
		t.Errorf("Merge reordered its argument: %v, was %v", in, keep)
	}
}
