package main

import (
	"fmt"
	"os"
	"strings"

	"fixture"
)

// check exercises Dedupe against its documented behaviour and exits non-zero
// when it disagrees.
func main() {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"a", "a", "b"}, "a,b"},
		{[]string{"a", "a", "b", "a"}, "a,b,a"},
		{[]string{"x", "y", "x", "y"}, "x,y,x,y"},
	}

	bad := 0
	for _, c := range cases {
		got := strings.Join(fixture.Dedupe(c.in), ",")
		if got != c.want {
			fmt.Printf("Dedupe(%v) = %q, want %q\n", c.in, got, c.want)
			bad++
		}
	}
	if bad > 0 {
		fmt.Printf("%d case(s) wrong\n", bad)
		os.Exit(1)
	}
	fmt.Println("ok")
}
