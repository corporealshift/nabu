package github

import (
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestParseReview(t *testing.T) {
	tests := []struct {
		name    string
		final   string
		ok      bool
		summary string
	}{
		{name: "one block", ok: true, summary: "fine",
			final: "```json\n{\"summary\":\"fine\",\"comments\":[]}\n```"},
		{name: "prose around it", ok: true, summary: "fine",
			final: "Looked at it.\n\n```json\n{\"summary\":\"fine\"}\n```\n\nThat's all."},
		{name: "last block wins", ok: true, summary: "second",
			final: "```json\n{\"summary\":\"first\"}\n```\nthen\n```json\n{\"summary\":\"second\"}\n```"},
		{name: "crlf", ok: true, summary: "fine",
			final: "```json\r\n{\"summary\":\"fine\"}\r\n```"},
		{name: "no block", final: "Looks good to me."},
		{name: "untagged block", final: "```\n{\"summary\":\"fine\"}\n```"},
		{name: "go block", final: "```go\nfunc f() {}\n```"},
		{name: "bad json", final: "```json\n{\"summary\": \n```"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, ok := ParseReview(tt.final)
			if ok != tt.ok || r.Summary != tt.summary {
				t.Errorf("ParseReview = %+v, %v; want summary %q, %v", r, ok, tt.summary, tt.ok)
			}
		})
	}
}

func readDiff(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/pr.diff")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func lineSet(m map[int]bool) []int {
	var out []int
	for n := range m {
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}

func TestRightLines(t *testing.T) {
	got := RightLines(readDiff(t))
	want := map[string][]int{
		"after.txt": {8, 9, 10, 11},                                                   // renamed, one line added
		"main.go":   {1, 2, 3, 4, 5, 6, 7, 8, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31}, // two hunks
		"new.go":    {1, 2},                                                           // new file
	}
	if len(got) != len(want) {
		t.Errorf("files = %v, want %v (a deleted file has no right side)", got, want)
	}
	for file, lines := range want {
		if g := lineSet(got[file]); !reflect.DeepEqual(g, lines) {
			t.Errorf("%s: lines = %v, want %v", file, g, lines)
		}
	}
}

func TestRightLinesCountsHunksNotPrefixes(t *testing.T) {
	// An added line whose text starts "++ " reads as "+++ " in the diff, and a
	// removed one starting "-- " as "--- ". Neither is a file header.
	diff := "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1,2 +1,2 @@\n--- old\n+++ new\n keep\n"
	got := RightLines(diff)
	if g := lineSet(got["x"]); !reflect.DeepEqual(g, []int{1, 2}) || len(got) != 1 {
		t.Errorf("lines = %v", got)
	}
}

func TestBuildReview(t *testing.T) {
	diff := readDiff(t)
	r := Review{Summary: "Two things.", Comments: []ReviewFinding{
		{Path: "main.go", Line: 3, Body: "added line"},
		{Path: "main.go", Line: 26, Body: "context line"},
		{Path: "main.go", Line: 15, Body: "between hunks"},
		{Path: "old.txt", Line: 1, Body: "deleted file"},
		{Path: "nowhere.go", Line: 1, Body: "not in the diff"},
		{Path: "new.go", Line: 2, Body: "  "},
	}}
	post := BuildReview(r, true, "ignored", diff, "abc1234def")

	if post.Event != "COMMENT" || post.CommitID != "abc1234def" {
		t.Errorf("event %q, commit %q", post.Event, post.CommitID)
	}
	var placed []string
	for _, c := range post.Comments {
		if c.Side != "RIGHT" || !strings.HasSuffix(c.Body, Marker) {
			t.Errorf("comment %+v: want side RIGHT and the marker", c)
		}
		placed = append(placed, c.Path+":"+strings.TrimSuffix(c.Body, "\n\n"+Marker))
	}
	if want := []string{"main.go:added line", "main.go:context line"}; !reflect.DeepEqual(placed, want) {
		t.Errorf("line comments = %v, want %v", placed, want)
	}
	for _, s := range []string{"Two things.", "`main.go:15` — between hunks", "`old.txt:1` — deleted file", "`nowhere.go:1` — not in the diff"} {
		if !strings.Contains(post.Body, s) {
			t.Errorf("body lacks %q:\n%s", s, post.Body)
		}
	}
	if strings.Contains(post.Body, "new.go") {
		t.Errorf("an empty finding reached the body:\n%s", post.Body)
	}
	if !strings.HasSuffix(post.Body, Marker) {
		t.Errorf("body does not end with the marker:\n%s", post.Body)
	}
}

func TestBuildReviewWithoutAParsedReview(t *testing.T) {
	post := BuildReview(Review{}, false, "  It looks fine.\n", "", "abc")
	if post.Body != "It looks fine.\n\n"+Marker || len(post.Comments) != 0 || post.Event != "COMMENT" {
		t.Errorf("post = %+v", post)
	}
}

func TestBuildReviewWithoutASummary(t *testing.T) {
	post := BuildReview(Review{}, true, "", "", "abc1234def")
	if post.Body != "Reviewed abc1234.\n\n"+Marker {
		t.Errorf("body = %q", post.Body)
	}
}
