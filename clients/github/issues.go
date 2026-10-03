package github

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Issue is an open issue, as gh lists it.
type Issue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	URL    string `json:"url"`
}

// maxBrief is how long a brief may be: a session description's limit
// (protocol spec 3.1).
const maxBrief = 16000

// IssueBrief is a run's brief from an issue: its title, which names the run,
// its body, and every comment on it that nabu did not post.
func IssueBrief(repo string, is Issue, comments []Comment) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n%s\n\n(From issue %s#%d: %s)\n", strings.TrimSpace(is.Title), strings.TrimSpace(is.Body), repo, is.Number, is.URL)
	first := true
	for _, c := range comments {
		if fromNabu(c) || strings.TrimSpace(c.Body) == "" {
			continue
		}
		if first {
			b.WriteString("\n## Comments on the issue\n")
			first = false
		}
		fmt.Fprintf(&b, "\n**%s:** %s\n", c.Author, strings.TrimSpace(c.Body))
	}
	text := b.String()
	if len(text) > maxBrief {
		const note = "\n\n(The issue was cut here: it is longer than a brief may be.)"
		text = text[:maxBrief-len(note)] + note
	}
	return text
}

// Fingerprint changes when an issue is edited or someone other than nabu
// comments on it, and not when nabu does. It is what decides whether a failed
// run is asked for again: going by the issue's updated time would have nabu's
// own report of the failure start it again, forever.
func Fingerprint(is Issue, comments []Comment) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s", is.Title, is.Body)
	for _, c := range comments {
		if !fromNabu(c) {
			fmt.Fprintf(h, "\x00%d", c.ID)
		}
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// IssueLabel names a run's issue on its home, in the form labels allow.
func IssueLabel(repo string, n int) string {
	return "issue:" + strings.ToLower(repo) + "/" + strconv.Itoa(n)
}

// runLabels is a home's labels with every run:* label replaced by one.
func runLabels(current []string, label string) []string {
	out := []string{}
	for _, l := range current {
		if !strings.HasPrefix(l, "run:") {
			out = append(out, l)
		}
	}
	return append(out, label)
}

// issues gives each labeled issue a run, and asks again for a failed one
// whose issue has changed. Runs take no slot here; their sessions take the
// runner's.
func (w *Watcher) issues(ctx context.Context, d Daemon, repo Repo, rs *RepoState) error {
	open, err := w.GH.OpenIssues(ctx, repo.Name, w.Cfg.Label)
	if err != nil {
		return fmt.Errorf("github: %s: listing issues: %w", repo.Name, err)
	}
	for _, is := range open {
		rec, known := rs.Issues[is.Number]
		var labels []string
		if known {
			if labels, err = d.Labels(ctx, rec.Home); err != nil {
				return err
			}
			if !slices.Contains(labels, "run:failed") {
				continue // running, or done
			}
		}
		comments, err := w.GH.IssueComments(ctx, repo.Name, is.Number)
		if err != nil {
			return fmt.Errorf("github: %s#%d: %w", repo.Name, is.Number, err)
		}
		seen := Fingerprint(is, comments)
		brief := IssueBrief(repo.Name, is, comments)
		if !known {
			home, err := d.CreateHome(ctx, repo.Clone, brief, []string{"run:requested", IssueLabel(repo.Name, is.Number)})
			if err != nil {
				return fmt.Errorf("github: %s#%d: %w", repo.Name, is.Number, err)
			}
			rs.Issues[is.Number] = IssueRun{Home: home, Seen: seen}
			w.handed++
			w.logf("%s#%d: issue handed to the runner as run %s", repo.Name, is.Number, home)
		} else if seen != rec.Seen {
			if err := d.Rerun(ctx, rec.Home, brief, runLabels(labels, "run:requested")); err != nil {
				return err
			}
			rs.Issues[is.Number] = IssueRun{Home: rec.Home, Seen: seen}
			w.handed++
			w.logf("%s#%d: issue changed since its run failed; run %s asked for again", repo.Name, is.Number, rec.Home)
		}
		if err := w.save(); err != nil {
			return err
		}
	}
	return nil
}
