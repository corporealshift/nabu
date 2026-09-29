package github

import (
	"sort"
	"strings"
	"time"
)

// The kinds of comment on a pull request. Each comes from its own endpoint,
// with its own sequence of IDs.
const (
	// CommentLine is a review comment on a line, from pulls/<n>/comments.
	CommentLine = "line"
	// CommentReview is the body of a submitted review, from pulls/<n>/reviews.
	CommentReview = "review"
	// CommentIssue is a comment in the PR's conversation, from issues/<n>/comments.
	CommentIssue = "issue"
)

// Comment is one comment on a pull request, of any kind.
type Comment struct {
	Kind   string
	ID     int64
	Author string
	Body   string
	// Path, Line and DiffHunk place a line comment; InReplyTo is the comment
	// a reply in a thread answers.
	Path      string
	Line      int
	DiffHunk  string
	InReplyTo int64
	Created   time.Time
	URL       string
}

// Root is the comment a line comment's thread starts from. GitHub takes
// replies only to that one.
func (c Comment) Root() int64 {
	if c.InReplyTo != 0 {
		return c.InReplyTo
	}
	return c.ID
}

// Marks holds one comment ID per kind: how far something has got through a
// PR's comments. IDs of different kinds are never compared.
type Marks struct {
	Line   int64 `json:"line,omitempty"`
	Review int64 `json:"review,omitempty"`
	Issue  int64 `json:"issue,omitempty"`
}

// Of is the mark for one kind.
func (m Marks) Of(kind string) int64 {
	switch kind {
	case CommentLine:
		return m.Line
	case CommentReview:
		return m.Review
	case CommentIssue:
		return m.Issue
	}
	return 0
}

// Raise moves a mark up to include a comment.
func (m *Marks) Raise(c Comment) {
	switch c.Kind {
	case CommentLine:
		m.Line = max(m.Line, c.ID)
	case CommentReview:
		m.Review = max(m.Review, c.ID)
	case CommentIssue:
		m.Issue = max(m.Issue, c.ID)
	}
}

// Merge raises every mark to at least the other's.
func (m *Marks) Merge(o Marks) {
	m.Line, m.Review, m.Issue = max(m.Line, o.Line), max(m.Review, o.Review), max(m.Issue, o.Issue)
}

// IsZero reports whether nothing has been marked.
func (m Marks) IsZero() bool { return m == Marks{} }

// fromNabu reports whether the watcher posted a comment. Kyle and nabu share a
// login, so the marker is the only way to tell.
func fromNabu(c Comment) bool { return strings.Contains(c.Body, Marker) }

// CommentsDue is the comments a job for this PR should answer now, oldest
// first, or nil if no job is due. pr must be open.
//
// A comment counts if nabu did not post it, it says something, and it is past
// the handled mark of its kind. A job is due once the newest counting comment
// has sat for the quiet period, so a review written as several comments is
// one job. After a failure, only a comment past where the failed job reached
// starts another, though that one still answers everything unhandled.
func CommentsDue(cfg Config, st *RepoState, pr PR, comments []Comment, now time.Time) []Comment {
	if !cfg.CommentsEnabled() || pr.Fork || !pr.HasLabel(cfg.Label) {
		return nil
	}
	for _, j := range st.Running {
		if j.Kind == KindComments && j.PR == pr.Number {
			return nil
		}
	}
	handled := st.Handled[pr.Number]
	failed, hasFailed := st.FailedThrough[pr.Number]
	var due []Comment
	var newest time.Time
	pastFailure := !hasFailed
	for _, c := range comments {
		if fromNabu(c) || strings.TrimSpace(c.Body) == "" || c.ID <= handled.Of(c.Kind) {
			continue
		}
		due = append(due, c)
		if c.Created.After(newest) {
			newest = c.Created
		}
		if hasFailed && c.ID > failed.Of(c.Kind) {
			pastFailure = true
		}
	}
	if len(due) == 0 || !pastFailure || now.Sub(newest) < cfg.QuietFor() {
		return nil
	}
	sortComments(due)
	return due
}

// sortComments orders comments by when they were written.
func sortComments(cs []Comment) {
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].Created.Before(cs[j].Created) })
}
