package github

import (
	"fmt"
	"strings"
)

// CommentsGoal is the goal a comments session is judged against.
const CommentsGoal = "Every comment listed in the first message has been addressed by a change or answered by a reply, " +
	"the changes are committed, and nothing is pushed."

// CommentsPrompt is the one message a comments session gets. due is what it
// must answer; all is every comment on the PR, from which each line
// comment's whole thread is shown so a reply reads in context.
func CommentsPrompt(repo string, pr PR, due, all []Comment) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Address the new comments on pull request #%d in %s: %q.\n\n", pr.Number, repo, pr.Title)
	fmt.Fprintf(&b, "This workspace is a detached worktree at the PR's head, %s, on branch %s. The base branch is %s.\n\n", pr.HeadSHA, pr.HeadRef, pr.BaseRef)
	body := strings.TrimSpace(pr.Body)
	switch {
	case body == "":
		body = "(no description)"
	case len(body) > maxBody:
		body = body[:maxBody] + "\n\n(description cut at 8000 characters)"
	}
	fmt.Fprintf(&b, "The description:\n\n%s\n\n", body)

	isDue := map[string]bool{}
	for _, c := range due {
		isDue[key(c)] = true
	}

	// Threads with something new in them, in the order their first new
	// comment was written.
	var roots []int64
	seen := map[int64]bool{}
	for _, c := range due {
		if c.Kind == CommentLine && !seen[c.Root()] {
			seen[c.Root()] = true
			roots = append(roots, c.Root())
		}
	}
	threads := map[int64][]Comment{}
	for _, c := range all {
		if c.Kind == CommentLine && seen[c.Root()] {
			threads[c.Root()] = append(threads[c.Root()], c)
		}
	}
	if len(roots) > 0 {
		b.WriteString("## Comments on lines\n\n")
	}
	for _, root := range roots {
		th := threads[root]
		sortComments(th)
		first := th[0]
		fmt.Fprintf(&b, "### %s:%d\n\n", first.Path, first.Line)
		if first.DiffHunk != "" {
			fmt.Fprintf(&b, "```diff\n%s\n```\n\n", strings.TrimRight(first.DiffHunk, "\n"))
		}
		for _, c := range th {
			fmt.Fprintf(&b, "%s\n\n", describe(c, isDue[key(c)]))
		}
	}

	var rest []Comment
	for _, c := range due {
		if c.Kind != CommentLine {
			rest = append(rest, c)
		}
	}
	if len(rest) > 0 {
		b.WriteString("## Comments on the whole PR\n\n")
		for _, c := range rest {
			fmt.Fprintf(&b, "%s\n\n", describe(c, true))
		}
	}

	b.WriteString(`## What to do

Answer every comment marked new, by id. Comments marked earlier are only there so the thread makes sense.

- Where a comment asks for a change, make it, and check it builds and its tests pass.
- Where it asks a question, or you think the change would be wrong, answer it instead and say why.
- Where you cannot tell what it wants, change nothing for it, and ask your question in its reply. The author answers on GitHub.
- Commit your changes, following the repository's conventions: read its CLAUDE.md or AGENTS.md, if it has one, for how commit messages are written. Do not push: pushing and posting your replies are done for you once you finish.
- Nobody is watching this session, so do not use the ask tool: a question would only wait ten minutes for an answer that never comes.

`)
	fmt.Fprintf(&b, "## Before you reply\n\nCheck each new comment against your own diff, `git diff %s`, one comment at a time:\n\n", pr.HeadSHA)
	b.WriteString(`- Find the lines the comment asked about and read what they say now. Where it gave exact wording, the file must have that wording, character for character: quote the line in your reply.
- Where it asked for part of something to move or go, check the rest is still there. Read every line your diff removes and be sure each was meant to go.
- Where you did only part of what it asked, say which part you did not do and why. Do not say a comment is addressed when it is not.

End your final message with exactly one fenced json block, with one reply for each new comment:

` + "```json" + `
{"replies": [{"id": 123456, "body": "Renamed it to parseCursor in a1b2c3d."}]}
` + "```" + `

Each reply is posted under the comment it answers, so say what you did, or what you need to know, in a sentence or two.
`)
	return b.String()
}

// describe is one comment as the prompt shows it.
func describe(c Comment, isNew bool) string {
	state := "earlier"
	if isNew {
		state = "new"
	}
	who := c.Author
	if fromNabu(c) {
		who = "nabu"
	}
	kind := "comment"
	if c.Kind == CommentReview {
		kind = "review"
	}
	text := strings.TrimSpace(strings.ReplaceAll(c.Body, Marker, ""))
	return fmt.Sprintf("**%s %s, id %d, by %s:**\n\n%s", state, kind, c.ID, who, text)
}

// key names a comment uniquely across kinds.
func key(c Comment) string { return fmt.Sprintf("%s-%d", c.Kind, c.ID) }

// ParseReplies decodes the replies block of a comments session's final
// message, by comment id.
func ParseReplies(final string) (map[int64]string, bool) {
	var out struct {
		Replies []struct {
			ID   int64  `json:"id"`
			Body string `json:"body"`
		} `json:"replies"`
	}
	if !decodeLastBlock(final, &out) {
		return nil, false
	}
	m := make(map[int64]string, len(out.Replies))
	for _, r := range out.Replies {
		if strings.TrimSpace(r.Body) != "" {
			m[r.ID] = strings.TrimSpace(r.Body)
		}
	}
	return m, true
}

// ThreadReply is a reply posted in a line comment's thread.
type ThreadReply struct {
	// Key names the reply in Job.Posted, so a retry does not post it twice.
	Key  string
	Root int64
	Body string
}

// Replies is everything a comments job posts: a reply in each thread it
// answered, and one comment on the PR for the rest.
type Replies struct {
	Threads      []ThreadReply
	Conversation string
}

// ConversationKey names the conversation comment in Job.Posted.
const ConversationKey = "conversation"

// maxQuote is how many lines of a comment a reply quotes.
const maxQuote = 6

// BuildReplies turns a finished session's answers into what to post. A line
// comment is answered in its thread; a review body or conversation comment is
// quoted and answered in one comment on the PR, which also lists anything
// left without its own answer. Without parsed replies, the final message is
// that one comment.
func BuildReplies(due []Comment, replies map[int64]string, ok bool, final string) Replies {
	var out Replies
	if !ok {
		out.Conversation = Signature + "\n\n" + strings.TrimSpace(final) + "\n\n" + Marker
		return out
	}
	var parts, unanswered []string
	for _, c := range due {
		reply, has := replies[c.ID]
		switch {
		case !has:
			unanswered = append(unanswered, link(c))
		case c.Kind == CommentLine:
			out.Threads = append(out.Threads, ThreadReply{
				Key: key(c), Root: c.Root(), Body: Signature + ": " + reply + "\n\n" + Marker,
			})
		default:
			parts = append(parts, fmt.Sprintf("On %s:\n\n%s\n\n%s", link(c), quote(c.Body), reply))
		}
	}
	if len(unanswered) > 0 {
		parts = append(parts, "Not answered individually: "+strings.Join(unanswered, ", ")+"; see the pushed commits.")
	}
	if len(parts) > 0 {
		out.Conversation = Signature + "\n\n" + strings.Join(parts, "\n\n---\n\n") + "\n\n" + Marker
	}
	return out
}

// link names a comment by its URL when there is one.
func link(c Comment) string {
	if c.URL == "" {
		return fmt.Sprintf("comment %d", c.ID)
	}
	word := "comment"
	if c.Kind == CommentReview {
		word = "review"
	}
	return fmt.Sprintf("[this %s](%s)", word, c.URL)
}

// quote is the start of a comment as a markdown quote.
func quote(body string) string {
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(body, "\r\n", "\n")), "\n")
	cut := len(lines) > maxQuote
	if cut {
		lines = lines[:maxQuote]
	}
	for i, l := range lines {
		lines[i] = "> " + l
	}
	if cut {
		lines = append(lines, "> …")
	}
	return strings.Join(lines, "\n")
}
