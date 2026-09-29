package github

import (
	"fmt"
	"strings"
)

// maxBody caps how much of a PR description goes in a prompt.
const maxBody = 8000

// ReviewPrompt is the one message a review session gets. prev is the review
// nabu last posted on this PR, if any, so a later review says what is new
// rather than repeating itself.
func ReviewPrompt(repo string, pr PR, prev *Reviewed) string {
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

	b.WriteString(`Read the change, and whatever code around it you need to judge it. Look for bugs, missing or weak tests, behaviour that does not match the description, and anything against the repository's own conventions. Say what is wrong and why; skip style points a formatter would settle.

This is a review only. Change no files: do not edit, create or delete anything, and do not commit or push. Posting the review is done for you once you finish.

Nobody is watching this session, so do not use the ask tool: a question would only wait ten minutes for an answer that never comes. Find out what you can from the repository yourself, and put anything you could not settle in the summary as a question for the author.

End your final message with exactly one fenced json block:

` + "```json" + `
{"summary": "what you found overall", "comments": [{"path": "src/file.rs", "line": 42, "body": "what is wrong here"}]}
` + "```" + `

path is relative to the repository root. line is a line number in the new version of the file, and must be one the diff adds or shows as context. Anything about other lines goes in the summary. If nothing is worth saying, say so in the summary and leave comments empty.
`)
	return b.String()
}
