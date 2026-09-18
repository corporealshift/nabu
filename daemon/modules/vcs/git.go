package vcs

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/corporealshift/nabu/daemon/module"
)

const gitDescription = "Read the repository's state as structured data rather than as text to " +
	"parse. op is one of: status (branch, ahead/behind, changed files), log (recent commits), " +
	"diff (what changed, with per-file counts and the patch), show (one commit), " +
	"blame (who last touched each line), commit (record the staged changes). " +
	"Prefer this to running git through bash: the answers come back already parsed."

var gitSchema = json.RawMessage(`{"type":"object","required":["op"],"properties":{
	"op":{"type":"string","enum":["status","log","diff","show","blame","commit"]},
	"path":{"type":"string","description":"limit to this file or directory; required for blame"},
	"ref":{"type":"string","description":"a commit, branch or range; the commit for show"},
	"staged":{"type":"boolean","description":"diff: compare the index rather than the worktree"},
	"limit":{"type":"integer","description":"log: how many commits, default 20"},
	"start":{"type":"integer","description":"blame: first line, 1-based"},
	"end":{"type":"integer","description":"blame: last line"},
	"message":{"type":"string","description":"commit: the message"}}}`)

type gitArgs struct {
	Op      string `json:"op"`
	Path    string `json:"path"`
	Ref     string `json:"ref"`
	Staged  bool   `json:"staged"`
	Limit   int    `json:"limit"`
	Start   int    `json:"start"`
	End     int    `json:"end"`
	Message string `json:"message"`
}

func (m *Module) runGit(ctx context.Context, s module.Session, raw json.RawMessage) (string, error) {
	var a gitArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", badArgs("invalid arguments: %s", err)
		}
	}
	switch a.Op {
	case "status":
		return m.gitStatus(ctx, s)
	case "log":
		return m.gitLog(ctx, s, a)
	case "diff":
		return m.gitDiff(ctx, s, a)
	case "show":
		return m.gitShow(ctx, s, a)
	case "blame":
		return m.gitBlame(ctx, s, a)
	case "commit":
		return m.gitCommit(ctx, s, a)
	case "":
		return "", badArgs("op is required")
	default:
		return "", badArgs("unknown op %q", a.Op)
	}
}

// ---------------------------------------------------------------- status

// StatusFile is one changed path. Index and Worktree are git's two-letter
// status kept apart, because staged and unstaged are different facts and
// flattening them is what makes porcelain output hard to read.
type StatusFile struct {
	Path     string `json:"path"`
	Index    string `json:"index,omitempty"`
	Worktree string `json:"worktree,omitempty"`
	Renamed  string `json:"renamed_from,omitempty"`
}

// Status is the working tree at a glance.
type Status struct {
	Branch    string       `json:"branch"`
	Upstream  string       `json:"upstream,omitempty"`
	Ahead     int          `json:"ahead"`
	Behind    int          `json:"behind"`
	Clean     bool         `json:"clean"`
	Files     []StatusFile `json:"files"`
	Untracked []string     `json:"untracked,omitempty"`
}

func (m *Module) gitStatus(ctx context.Context, s module.Session) (string, error) {
	// porcelain=v2 is the documented stable format. v1 is the one that looks
	// easy and then loses rename information.
	out, err := m.exec(ctx, s, m.gitPath, "status", "--porcelain=v2", "--branch", "-z")
	if err != nil {
		return "", err
	}
	st := parseStatus(out)
	return m.reply(st)
}

func parseStatus(out string) Status {
	st := Status{Files: []StatusFile{}}
	for _, rec := range strings.Split(out, "\x00") {
		if rec == "" {
			continue
		}
		switch {
		case strings.HasPrefix(rec, "# branch.head "):
			st.Branch = strings.TrimPrefix(rec, "# branch.head ")
		case strings.HasPrefix(rec, "# branch.upstream "):
			st.Upstream = strings.TrimPrefix(rec, "# branch.upstream ")
		case strings.HasPrefix(rec, "# branch.ab "):
			// "+2 -1": ahead 2, behind 1.
			for _, f := range strings.Fields(strings.TrimPrefix(rec, "# branch.ab ")) {
				n, err := strconv.Atoi(f[1:])
				if err != nil {
					continue
				}
				if f[0] == '+' {
					st.Ahead = n
				} else {
					st.Behind = n
				}
			}
		case strings.HasPrefix(rec, "1 "), strings.HasPrefix(rec, "2 "):
			if f, ok := parseChangedEntry(rec); ok {
				st.Files = append(st.Files, f)
			}
		case strings.HasPrefix(rec, "? "):
			st.Untracked = append(st.Untracked, strings.TrimPrefix(rec, "? "))
		}
	}
	st.Clean = len(st.Files) == 0 && len(st.Untracked) == 0
	return st
}

// parseChangedEntry reads a "1" (ordinary) or "2" (renamed) porcelain v2 line.
// Both put the XY status in field 1 and the path last.
func parseChangedEntry(rec string) (StatusFile, bool) {
	fields := strings.SplitN(rec, " ", 9)
	if len(fields) < 9 {
		return StatusFile{}, false
	}
	xy := fields[1]
	if len(xy) < 2 {
		return StatusFile{}, false
	}
	f := StatusFile{
		Index:    strings.TrimSpace(string(xy[0])),
		Worktree: strings.TrimSpace(string(xy[1])),
	}
	// A rename entry's last field is "<score>\t<path>"; the old path arrives as
	// the next NUL-separated record, which the caller has already split away.
	// Keeping the score out of the path matters more than recovering the old
	// name, which `renamed_from` carries only when git inlines it with a tab.
	last := fields[8]
	if strings.HasPrefix(rec, "2 ") {
		if tab := strings.IndexByte(last, '\t'); tab >= 0 {
			f.Renamed, last = last[tab+1:], last[:tab]
		}
		if sp := strings.IndexByte(last, ' '); sp >= 0 {
			last = last[sp+1:]
		}
	}
	f.Path = last
	return f, true
}

// ---------------------------------------------------------------- log

// Commit is one entry of the history.
type Commit struct {
	SHA     string `json:"sha"`
	Short   string `json:"short"`
	Author  string `json:"author"`
	Date    string `json:"date"`
	Subject string `json:"subject"`
}

// logFormat uses unit separators, because a subject may contain anything a
// person can type and splitting on spaces or pipes loses to the first commit
// message that contains one.
const logFormat = "%H\x1f%h\x1f%an\x1f%aI\x1f%s"

func (m *Module) gitLog(ctx context.Context, s module.Session, a gitArgs) (string, error) {
	limit := a.Limit
	if limit <= 0 {
		limit = defaultLogLimit
	}
	if limit > maxLogLimit {
		return "", badArgs("limit %d is above the maximum of %d", limit, maxLogLimit)
	}
	args := []string{"log", "--pretty=format:" + logFormat, "-n", strconv.Itoa(limit)}
	if a.Ref != "" {
		args = append(args, a.Ref)
	}
	if a.Path != "" {
		args = append(args, "--", a.Path)
	}
	out, err := m.exec(ctx, s, m.gitPath, args...)
	if err != nil {
		return "", err
	}
	return m.reply(map[string]any{"commits": parseLog(out)})
}

func parseLog(out string) []Commit {
	commits := []Commit{}
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, "\x1f")
		if len(f) < 5 {
			continue
		}
		commits = append(commits, Commit{
			SHA: f[0], Short: f[1], Author: f[2], Date: f[3], Subject: f[4],
		})
	}
	return commits
}

// ---------------------------------------------------------------- diff

// FileChange is one file's shape of change. Added and Removed are -1 for a
// binary file, which has counts git will not give.
type FileChange struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	Binary  bool   `json:"binary,omitempty"`
}

// Diff pairs the shape of a change with the change itself. The counts are what
// a model usually wants; the patch is there when it needs to read the edit.
type Diff struct {
	Files   []FileChange `json:"files"`
	Added   int          `json:"added_total"`
	Removed int          `json:"removed_total"`
	Patch   string       `json:"patch"`
}

func (m *Module) gitDiff(ctx context.Context, s module.Session, a gitArgs) (string, error) {
	base := []string{"diff"}
	if a.Staged {
		base = append(base, "--cached")
	}
	if a.Ref != "" {
		base = append(base, a.Ref)
	}
	tail := []string{}
	if a.Path != "" {
		tail = append(tail, "--", a.Path)
	}

	stat, err := m.exec(ctx, s, m.gitPath, append(append(append([]string{}, base...), "--numstat"), tail...)...)
	if err != nil {
		return "", err
	}
	patch, err := m.exec(ctx, s, m.gitPath, append(append([]string{}, base...), tail...)...)
	if err != nil {
		return "", err
	}
	d := parseNumstat(stat)
	d.Patch = patch
	return m.reply(d)
}

func parseNumstat(out string) Diff {
	d := Diff{Files: []FileChange{}}
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		f := strings.SplitN(strings.TrimSpace(line), "\t", 3)
		if len(f) < 3 {
			continue
		}
		fc := FileChange{Path: f[2]}
		// git writes "-" for both counts on a binary file. Reporting 0 there
		// would say "nothing changed", which is a different and wrong claim.
		if f[0] == "-" || f[1] == "-" {
			fc.Binary, fc.Added, fc.Removed = true, -1, -1
		} else {
			fc.Added, _ = strconv.Atoi(f[0])
			fc.Removed, _ = strconv.Atoi(f[1])
			d.Added += fc.Added
			d.Removed += fc.Removed
		}
		d.Files = append(d.Files, fc)
	}
	return d
}

// ---------------------------------------------------------------- show

func (m *Module) gitShow(ctx context.Context, s module.Session, a gitArgs) (string, error) {
	ref := a.Ref
	if ref == "" {
		ref = "HEAD"
	}
	meta, err := m.exec(ctx, s, m.gitPath, "show", "--no-patch", "--pretty=format:"+logFormat, ref)
	if err != nil {
		return "", err
	}
	commits := parseLog(meta)
	if len(commits) == 0 {
		return "", badArgs("no commit at %q", ref)
	}
	stat, err := m.exec(ctx, s, m.gitPath, "show", "--numstat", "--pretty=format:", ref)
	if err != nil {
		return "", err
	}
	patch, err := m.exec(ctx, s, m.gitPath, "show", "--pretty=format:", ref)
	if err != nil {
		return "", err
	}
	d := parseNumstat(stat)
	d.Patch = patch
	return m.reply(map[string]any{"commit": commits[0], "diff": d})
}

// ---------------------------------------------------------------- blame

// BlameLine is one line and the commit that last touched it.
type BlameLine struct {
	Line   int    `json:"line"`
	SHA    string `json:"sha"`
	Author string `json:"author"`
	Date   string `json:"date"`
	Text   string `json:"text"`
}

func (m *Module) gitBlame(ctx context.Context, s module.Session, a gitArgs) (string, error) {
	if a.Path == "" {
		return "", badArgs("blame needs a path")
	}
	args := []string{"blame", "--line-porcelain"}
	if a.Start > 0 || a.End > 0 {
		start, end := a.Start, a.End
		if start <= 0 {
			start = 1
		}
		if end <= 0 {
			end = start
		}
		if end < start {
			return "", badArgs("end line %d is before start line %d", end, start)
		}
		args = append(args, "-L", strconv.Itoa(start)+","+strconv.Itoa(end))
	}
	if a.Ref != "" {
		args = append(args, a.Ref)
	}
	args = append(args, "--", a.Path)

	out, err := m.exec(ctx, s, m.gitPath, args...)
	if err != nil {
		return "", err
	}
	return m.reply(map[string]any{"path": a.Path, "lines": parseBlame(out)})
}

// parseBlame reads --line-porcelain: a header line with the sha and the line
// number, then key/value lines, then the content line prefixed with a tab.
func parseBlame(out string) []BlameLine {
	lines := []BlameLine{}
	var cur BlameLine
	var have bool
	for _, raw := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		switch {
		case strings.HasPrefix(raw, "\t"):
			if have {
				cur.Text = strings.TrimPrefix(raw, "\t")
				lines = append(lines, cur)
				cur, have = BlameLine{}, false
			}
		case strings.HasPrefix(raw, "author "):
			cur.Author = strings.TrimPrefix(raw, "author ")
		case strings.HasPrefix(raw, "author-time "):
			cur.Date = strings.TrimPrefix(raw, "author-time ")
		default:
			f := strings.Fields(raw)
			// A header is "<sha> <orig-line> <final-line> [<count>]".
			if len(f) >= 3 && len(f[0]) == 40 && !strings.Contains(f[0], " ") {
				if n, err := strconv.Atoi(f[2]); err == nil {
					cur.SHA, cur.Line, have = f[0], n, true
				}
			}
		}
	}
	return lines
}

// ---------------------------------------------------------------- commit

func (m *Module) gitCommit(ctx context.Context, s module.Session, a gitArgs) (string, error) {
	if strings.TrimSpace(a.Message) == "" {
		return "", badArgs("commit needs a message")
	}
	// Only what is already staged. Staging is a decision about what belongs in
	// the commit, and a tool that quietly ran "add -A" would make that decision
	// on the model's behalf and sweep up whatever else was in the tree.
	if _, err := m.exec(ctx, s, m.gitPath, "diff", "--cached", "--quiet"); err == nil {
		return "", badArgs("nothing is staged; stage the files this commit should contain first")
	}
	if _, err := m.exec(ctx, s, m.gitPath, "commit", "-m", a.Message); err != nil {
		return "", err
	}
	meta, err := m.exec(ctx, s, m.gitPath, "show", "--no-patch", "--pretty=format:"+logFormat, "HEAD")
	if err != nil {
		return "", err
	}
	commits := parseLog(meta)
	if len(commits) == 0 {
		return "", badArgs("the commit was made but could not be read back")
	}
	return m.reply(map[string]any{"committed": commits[0]})
}
