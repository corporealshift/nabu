package format

import "testing"

func TestTableAlignsColumns(t *testing.T) {
	got := Table([][]string{{"a", "b"}, {"cc", "dd"}}, []int{4, 4})
	want := "a   b   \ncc  dd  \n"
	if got != want {
		t.Errorf("Table() = %q, want %q", got, want)
	}
}
