package github

import (
	"sort"
	"strings"
	"time"
)

// PR is an open pull request, as the poll sees it.
type PR struct {
	Number  int
	HeadSHA string
	HeadRef string
	BaseRef string
	Title   string
	Body    string
	Draft   bool
	// Fork is true when the head lives in another repository.
	Fork   bool
	Labels []string
}

// HasLabel reports whether the PR carries a label.
func (p PR) HasLabel(name string) bool {
	for _, l := range p.Labels {
		if strings.EqualFold(l, name) {
			return true
		}
	}
	return false
}

// Observe records when each open PR's head was first seen. A new head starts
// its quiet period again, and a PR no longer open is forgotten.
func Observe(st *RepoState, prs []PR, now time.Time) {
	open := make(map[int]bool, len(prs))
	for _, p := range prs {
		open[p.Number] = true
		if s, ok := st.Seen[p.Number]; !ok || s.SHA != p.HeadSHA {
			st.Seen[p.Number] = Seen{SHA: p.HeadSHA, At: now}
		}
	}
	for n := range st.Seen {
		if !open[n] {
			delete(st.Seen, n)
		}
	}
}

// ReviewJobs is every PR that should be reviewed now, oldest first. Observe
// must have run on the same list first.
//
// This is where the rules live, as a pure function of the snapshot, the
// state and the clock, so they are tested without gh, git or a daemon.
func ReviewJobs(cfg Config, st *RepoState, prs []PR, now time.Time) []PR {
	if !cfg.ReviewEnabled() {
		return nil
	}
	running := map[int]bool{}
	for _, j := range st.Running {
		running[j.PR] = true
	}
	var out []PR
	for _, p := range prs {
		switch {
		case p.Draft, p.Fork, running[p.Number]:
			continue
		case st.Failed[p.Number] == p.HeadSHA:
			continue
		}
		if r, ok := st.Reviewed[p.Number]; ok && (r.SHA == p.HeadSHA || !cfg.ReviewPushes()) {
			continue
		}
		if s, ok := st.Seen[p.Number]; !ok || s.SHA != p.HeadSHA || now.Sub(s.At) < cfg.QuietFor() {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out
}

// Admit is the part of the queue that may start while running jobs are
// already going, so the total stays within maxJobs.
func Admit[T any](maxJobs, running int, queued []T) []T {
	free := maxJobs - running
	if free <= 0 {
		return nil
	}
	if free < len(queued) {
		return queued[:free]
	}
	return queued
}
