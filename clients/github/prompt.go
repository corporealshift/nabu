package github

import (
	"fmt"
	"strings"
)

// maxBody caps how much of a PR description goes in a prompt.
const maxBody = 8000

// maxAsked caps how much of the answered comments go in a review prompt.
const maxAsked = 8000

// Answered is the comments a person wrote that comments jobs answered
// between two marks, oldest first: what the commits since the last review
// were asked to do.
func Answered(from, to Marks, all []Comment) []Comment {
	var out []Comment
	for _, c := range all {
		if fromNabu(c) || strings.TrimSpace(c.Body) == "" {
			continue
		}
		if c.ID > from.Of(c.Kind) && c.ID <= to.Of(c.Kind) {
			out = append(out, c)
		}
	}
	sortComments(out)
	return out
}

// ReviewPrompt is the one message a review session gets. prev is the review
// nabu last posted on this PR, if any, so a later review says what is new
// rather than repeating itself. asked is the comments answered since prev,
// so the review can check the new commits against what they asked for.
func ReviewPrompt(repo string, pr PR, prev *Reviewed, asked []Comment) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Review pull request #%d in %s: %q.\n\n", pr.Number, repo, pr.Title)
	fmt.Fprintf(&b, "This workspace is a detached worktree at the PR's head, %s. The base branch is %s.\n", pr.HeadSHA, pr.BaseRef)
	fmt.Fprintf(&b, "The change under review is `git diff origin/%s...%s`.\n\n", pr.BaseRef, pr.HeadSHA)

	body := strings.TrimSpace(pr.Body)
	switch {
	case body == "":
		body = "(no description)"
	case len(body) > maxBody:
		body = body[:maxBody] + "\n\n(description cut at 8000 characters)"
	}
	fmt.Fprintf(&b, "The description:\n\n%s\n\n", body)

	if prev != nil {
		fmt.Fprintf(&b, "You reviewed this PR before, at %s. Your summary then:\n\n%s\n\n", short(prev.SHA), strings.TrimSpace(prev.Summary))
		fmt.Fprintf(&b, "What changed since is `git diff %s..%s`. Say what is new, and what the new commits did or did not fix. Do not repeat points that were dealt with.\n\n", prev.SHA, pr.HeadSHA)
	}
	if len(asked) > 0 {
		b.WriteString("Since then, nabu answered these comments on the PR, and the new commits are its answer:\n\n")
		n := 0
		for _, c := range asked {
			text := describeAsked(c)
			if n += len(text); n > maxAsked {
				b.WriteString("(more comments, cut at 8000 characters)\n\n")
				break
			}
			b.WriteString(text + "\n\n")
		}
		b.WriteString("Check each one against the new commits. Did they do what it asked, all of it? Where it gave exact wording, does the file now say exactly that? Where it asked for part of something to move or go, is the rest still there? Name any comment the commits missed, did only in part, or did differently from what was asked.\n\n")
	}

	b.WriteString(`Read the change, and whatever code around it you need to judge it. Look for bugs, missing or weak tests, behaviour that does not match the description, and anything against the repository's own conventions. Say what is wrong and why; skip style points a formatter would settle.

This is a review only. Change no files: do not edit, create or delete anything, and do not commit or push. Posting the review is done for you once you finish.

Nobody is watching this session, so there is no one to ask. Find out what you can from the repository yourself; if you are stuck on something, look it up first: web.search finds documentation and answers, and web.fetch reads a page. If you have tried that and still cannot settle it, ask Claude with claude.ask, saying what you tried and what you are choosing between. Put anything still unsettled in the summary as a question for the author.

End your final message with exactly one fenced json block:

` + "```json" + `
{"summary": "what you found overall", "comments": [{"path": "src/file.rs", "line": 42, "body": "what is wrong here"}]}
` + "```" + `

path is relative to the repository root. line is a line number in the new version of the file, and must be one the diff adds or shows as context. Anything about other lines goes in the summary. If nothing is worth saying, say so in the summary and leave comments empty.
`)
	return b.String()
}

// describeAsked is one answered comment as a review prompt shows it.
func describeAsked(c Comment) string {
	where := "on the whole PR"
	switch {
	case c.Kind == CommentLine && c.Path != "":
		where = fmt.Sprintf("on %s:%d", c.Path, c.Line)
	case c.Kind == CommentLine:
		where = "in a thread on a line"
	}
	return fmt.Sprintf("**%s, %s:**\n\n%s", c.Author, where, strings.TrimSpace(c.Body))
}
