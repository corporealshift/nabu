package github

import (
	"testing"
	"time"
)

func TestCommentsDue(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	old := now.Add(-time.Hour)
	f := false
	base := Config{}
	base.withDefaults()
	off := base
	off.Comments.Enabled = &f

	labeled := PR{Number: 7, HeadSHA: "abc", HeadRef: "feat", Labels: []string{"bug", "Nabu"}}
	line := Comment{Kind: CommentLine, ID: 100, Body: "rename this", Created: old}
	review := Comment{Kind: CommentReview, ID: 50, Body: "a few things", Created: old}
	issue := Comment{Kind: CommentIssue, ID: 900, Body: "and the README", Created: old}

	tests := []struct {
		name     string
		cfg      Config
		pr       PR
		comments []Comment
		setup    func(r *RepoState)
		want     int
	}{
		{name: "all three kinds", cfg: base, pr: labeled, comments: []Comment{line, review, issue}, want: 3},
		{name: "comments off", cfg: off, pr: labeled, comments: []Comment{line}},
		{name: "no label", cfg: base, pr: PR{Number: 7}, comments: []Comment{line}},
		{name: "fork", cfg: base, pr: PR{Number: 7, Labels: []string{"nabu"}, Fork: true}, comments: []Comment{line}},
		{name: "job running", cfg: base, pr: labeled, comments: []Comment{line},
			setup: func(r *RepoState) { r.Running = []Job{{Kind: KindComments, PR: 7}} }},
		{name: "a review job running does not block", cfg: base, pr: labeled, comments: []Comment{line}, want: 1,
			setup: func(r *RepoState) { r.Running = []Job{{Kind: KindReview, PR: 7}} }},
		{name: "nabu's own comments", cfg: base, pr: labeled,
			comments: []Comment{{Kind: CommentLine, ID: 101, Body: Signature + ": x\n\n" + Marker, Created: old}}},
		{name: "an empty review body", cfg: base, pr: labeled,
			comments: []Comment{{Kind: CommentReview, ID: 51, Body: "  ", Created: old}}},
		{name: "already handled", cfg: base, pr: labeled, comments: []Comment{line, review},
			setup: func(r *RepoState) { r.Handled[7] = Marks{Line: 100, Review: 50} }},
		{name: "marks are per kind", cfg: base, pr: labeled, comments: []Comment{line, review}, want: 1,
			// A review ID below the line mark is still new: the IDs are not comparable.
			setup: func(r *RepoState) { r.Handled[7] = Marks{Line: 100} }},
		{name: "inside the quiet period", cfg: base, pr: labeled,
			comments: []Comment{line, {Kind: CommentIssue, ID: 901, Body: "one more", Created: now.Add(-time.Minute)}}},
		{name: "failed, nothing newer", cfg: base, pr: labeled, comments: []Comment{line, issue},
			setup: func(r *RepoState) { r.FailedThrough[7] = Marks{Line: 100, Issue: 900} }},
		{name: "failed, then a newer comment", cfg: base, pr: labeled, want: 3,
			comments: []Comment{line, issue, {Kind: CommentIssue, ID: 950, Body: "try again", Created: old}},
			setup:    func(r *RepoState) { r.FailedThrough[7] = Marks{Line: 100, Issue: 900} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := (&State{}).Repo("a/b")
			if tt.setup != nil {
				tt.setup(r)
			}
			if got := CommentsDue(tt.cfg, r, tt.pr, tt.comments, now); len(got) != tt.want {
				t.Errorf("due = %d comments, want %d: %+v", len(got), tt.want, got)
			}
		})
	}
}

func TestCommentsDueOldestFirst(t *testing.T) {
	now := time.Now()
	cfg := Config{}
	cfg.withDefaults()
	r := (&State{}).Repo("a/b")
	cs := []Comment{
		{Kind: CommentIssue, ID: 3, Body: "c", Created: now.Add(-time.Hour)},
		{Kind: CommentLine, ID: 9, Body: "a", Created: now.Add(-3 * time.Hour)},
		{Kind: CommentReview, ID: 1, Body: "b", Created: now.Add(-2 * time.Hour)},
	}
	got := CommentsDue(cfg, r, PR{Number: 1, Labels: []string{"nabu"}}, cs, now)
	if len(got) != 3 || got[0].Body != "a" || got[1].Body != "b" || got[2].Body != "c" {
		t.Errorf("order = %+v", got)
	}
}

func TestMarks(t *testing.T) {
	var m Marks
	m.Raise(Comment{Kind: CommentLine, ID: 5})
	m.Raise(Comment{Kind: CommentLine, ID: 3})
	m.Raise(Comment{Kind: CommentIssue, ID: 7})
	if m != (Marks{Line: 5, Issue: 7}) || m.Of(CommentLine) != 5 || m.Of("other") != 0 {
		t.Errorf("marks = %+v", m)
	}
	m.Merge(Marks{Line: 4, Review: 2})
	if m != (Marks{Line: 5, Review: 2, Issue: 7}) {
		t.Errorf("merged = %+v", m)
	}
	if (Comment{ID: 4, InReplyTo: 2}).Root() != 2 || (Comment{ID: 4}).Root() != 4 {
		t.Error("Root")
	}
}

func TestHasLabelIgnoresCase(t *testing.T) {
	if !(PR{Labels: []string{"NaBu"}}).HasLabel("nabu") || (PR{}).HasLabel("nabu") {
		t.Error("HasLabel")
	}
}
