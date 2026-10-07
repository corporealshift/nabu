package github

import (
	"strings"
	"testing"
	"time"
)

var (
	t0       = time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	root     = Comment{Kind: CommentLine, ID: 10, Author: "kyle", Body: "Why a map here?", Path: "a.go", Line: 4, DiffHunk: "@@ -1,3 +1,4 @@\n+m := map[string]int{}", Created: t0, URL: "u10"}
	nabuAns  = Comment{Kind: CommentLine, ID: 11, Author: "kyle", Body: Signature + ": For lookups.\n\n" + Marker, InReplyTo: 10, Created: t0.Add(time.Minute)}
	followUp = Comment{Kind: CommentLine, ID: 12, Author: "kyle", Body: "Use a slice, it's three items.", InReplyTo: 10, Created: t0.Add(2 * time.Minute), URL: "u12"}
	fresh    = Comment{Kind: CommentLine, ID: 20, Author: "kyle", Body: "Rename to parseCursor.", Path: "b.go", Line: 9, Created: t0.Add(3 * time.Minute), URL: "u20"}
	rev      = Comment{Kind: CommentReview, ID: 5, Author: "kyle", Body: "Mostly good.\nTwo things.", Created: t0.Add(4 * time.Minute), URL: "u5"}
	conv     = Comment{Kind: CommentIssue, ID: 900, Author: "kyle", Body: "Update the README too.", Created: t0.Add(5 * time.Minute), URL: "u900"}
)

func TestCommentsPrompt(t *testing.T) {
	pr := PR{Number: 7, Title: "Cursor", HeadSHA: "abc", HeadRef: "feat", BaseRef: "main"}
	all := []Comment{root, nabuAns, followUp, fresh, rev, conv}
	due := []Comment{followUp, fresh, rev, conv}
	p := CommentsPrompt("kyle/bw", pr, due, all)

	for _, s := range []string{
		`"Cursor"`, "branch feat",
		"### a.go:4", "+m := map[string]int{}",
		"**earlier comment, id 10, by kyle:**\n\nWhy a map here?",
		"**earlier comment, id 11, by nabu:**", // nabu's own reply is shown, attributed to nabu
		"**new comment, id 12, by kyle:**",
		"### b.go:9", "**new comment, id 20, by kyle:**",
		"## Comments on the whole PR", "**new review, id 5, by kyle:**", "**new comment, id 900, by kyle:**",
		"Do not push", "the ask tool reaches no person", `{"replies": [{"id": 123456`,
		// The check against its own diff, before it claims anything is done.
		"CLAUDE.md", "your own diff, `git diff abc`", "character for character", "every line your diff removes",
	} {
		if !strings.Contains(p, s) {
			t.Errorf("prompt lacks %q", s)
		}
	}
	if strings.Contains(p, Marker) {
		t.Error("the prompt shows the marker")
	}
	if strings.Index(p, "a.go:4") > strings.Index(p, "b.go:9") {
		t.Error("threads are not in the order their new comments were written")
	}
	if strings.Count(p, "id 10,") != 1 {
		t.Error("a thread root was shown more than once")
	}
}

func TestParseReplies(t *testing.T) {
	got, ok := ParseReplies("Done.\n```json\n{\"replies\":[{\"id\":12,\"body\":\" Now a slice. \"},{\"id\":20,\"body\":\"\"}]}\n```")
	if !ok || len(got) != 1 || got[12] != "Now a slice." {
		t.Errorf("replies = %v, %v", got, ok)
	}
	if _, ok := ParseReplies("I fixed everything."); ok {
		t.Error("no block parsed")
	}
}

func TestBuildReplies(t *testing.T) {
	due := []Comment{followUp, fresh, rev, conv}
	r := BuildReplies(due, map[int64]string{12: "Now a slice.", 5: "Both done.", 99: "not a due comment"}, true, "")

	if len(r.Threads) != 1 {
		t.Fatalf("threads = %+v", r.Threads)
	}
	th := r.Threads[0]
	if th.Root != 10 || th.Key != "line-12" || th.Body != Signature+": Now a slice.\n\n"+Marker {
		t.Errorf("a reply to a reply should go to the thread root: %+v", th)
	}
	for _, s := range []string{
		Signature + "\n\n",
		"On [this review](u5):\n\n> Mostly good.\n> Two things.\n\nBoth done.",
		"Not answered individually: [this comment](u20), [this comment](u900); see the pushed commits.",
	} {
		if !strings.Contains(r.Conversation, s) {
			t.Errorf("conversation lacks %q:\n%s", s, r.Conversation)
		}
	}
	if !strings.HasSuffix(r.Conversation, Marker) || strings.Contains(r.Conversation, "not a due comment") {
		t.Errorf("conversation:\n%s", r.Conversation)
	}
}

func TestBuildRepliesWithOnlyThreadAnswers(t *testing.T) {
	r := BuildReplies([]Comment{fresh}, map[int64]string{20: "Renamed."}, true, "")
	if r.Conversation != "" || len(r.Threads) != 1 {
		t.Errorf("replies = %+v", r)
	}
}

func TestBuildRepliesWithoutParsedReplies(t *testing.T) {
	r := BuildReplies([]Comment{fresh}, nil, false, "  I renamed it.\n")
	if len(r.Threads) != 0 || r.Conversation != Signature+"\n\nI renamed it.\n\n"+Marker {
		t.Errorf("replies = %+v", r)
	}
}

func TestQuoteCutsLongComments(t *testing.T) {
	q := quote(strings.Repeat("line\n", 10))
	if strings.Count(q, "> line") != maxQuote || !strings.HasSuffix(q, "> …") {
		t.Errorf("quote:\n%s", q)
	}
}
