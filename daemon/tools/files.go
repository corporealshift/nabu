package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/corporealshift/nabu/daemon/module"
)

// skipDir names directories never walked by glob and grep.
var skipDir = map[string]bool{".git": true, "node_modules": true}

func (b *Builtins) readTool() module.Tool {
	type args struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	return module.Tool{
		Name:        "read",
		Description: "Read a text file. Returns numbered lines. offset is the 1-based first line, limit the number of lines (default 2000).",
		Schema: schema(`{"type":"object","required":["path"],"properties":{
			"path":{"type":"string"},"offset":{"type":"integer"},"limit":{"type":"integer"}}}`),
		Run: func(ctx context.Context, s module.Session, raw json.RawMessage) (string, error) {
			a, err := decode[args](raw)
			if err != nil {
				return "", err
			}
			p, err := resolve(s, a.Path)
			if err != nil {
				return "", err
			}
			f, err := os.Open(p)
			if err != nil {
				return "", err
			}
			defer f.Close()
			if a.Offset < 1 {
				a.Offset = 1
			}
			if a.Limit < 1 {
				a.Limit = 2000
			}
			var sb strings.Builder
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
			n, shown := 0, 0
			for sc.Scan() {
				n++
				if n < a.Offset {
					continue
				}
				if shown >= a.Limit {
					fmt.Fprintf(&sb, "[... more lines; continue with offset %d ...]\n", n)
					break
				}
				fmt.Fprintf(&sb, "%d\t%s\n", n, sc.Text())
				shown++
			}
			if err := sc.Err(); err != nil {
				return "", err
			}
			if shown == 0 && n == 0 {
				return "(empty file)", nil
			}
			return truncate(sb.String(), b.maxOutput()), nil
		},
	}
}

func (b *Builtins) writeTool() module.Tool {
	type args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	return module.Tool{
		Name:        "write",
		Description: "Create or overwrite a file with the given content, creating parent directories.",
		Schema: schema(`{"type":"object","required":["path","content"],"properties":{
			"path":{"type":"string"},"content":{"type":"string"}}}`),
		Run: func(ctx context.Context, s module.Session, raw json.RawMessage) (string, error) {
			a, err := decode[args](raw)
			if err != nil {
				return "", err
			}
			p, err := resolve(s, a.Path)
			if err != nil {
				return "", err
			}
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return "", err
			}
			if err := os.WriteFile(p, []byte(a.Content), 0o644); err != nil {
				return "", err
			}
			return fmt.Sprintf("wrote %d bytes to %s", len(a.Content), rel(s, p)), nil
		},
	}
}

func (b *Builtins) editTool() module.Tool {
	type args struct {
		Path       string `json:"path"`
		Old        string `json:"old"`
		New        string `json:"new"`
		ReplaceAll bool   `json:"replace_all"`
	}
	return module.Tool{
		Name:        "edit",
		Description: "Replace text in a file. old must occur exactly once unless replace_all is true.",
		Schema: schema(`{"type":"object","required":["path","old","new"],"properties":{
			"path":{"type":"string"},"old":{"type":"string"},"new":{"type":"string"},"replace_all":{"type":"boolean"}}}`),
		Run: func(ctx context.Context, s module.Session, raw json.RawMessage) (string, error) {
			a, err := decode[args](raw)
			if err != nil {
				return "", err
			}
			if a.Old == "" {
				return "", fmt.Errorf("old must not be empty")
			}
			p, err := resolve(s, a.Path)
			if err != nil {
				return "", err
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return "", err
			}
			text := string(data)
			n := strings.Count(text, a.Old)
			switch {
			case n == 0:
				return "", fmt.Errorf("old text not found in %s", rel(s, p))
			case n > 1 && !a.ReplaceAll:
				return "", fmt.Errorf("old text occurs %d times in %s; make it unique or set replace_all", n, rel(s, p))
			}
			if !a.ReplaceAll {
				n = 1
			}
			text = strings.Replace(text, a.Old, a.New, n)
			if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
				return "", err
			}
			return fmt.Sprintf("replaced %d occurrence(s) in %s", n, rel(s, p)), nil
		},
	}
}

func (b *Builtins) globTool() module.Tool {
	type args struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	return module.Tool{
		Name:        "glob",
		Description: "Find files by glob pattern (supports **). path is the directory to search, default the workspace.",
		Schema: schema(`{"type":"object","required":["pattern"],"properties":{
			"pattern":{"type":"string"},"path":{"type":"string"}}}`),
		Run: func(ctx context.Context, s module.Session, raw json.RawMessage) (string, error) {
			a, err := decode[args](raw)
			if err != nil {
				return "", err
			}
			if a.Pattern == "" {
				return "", fmt.Errorf("pattern is required")
			}
			root := s.Workspace().Path
			if a.Path != "" {
				if root, err = resolve(s, a.Path); err != nil {
					return "", err
				}
			}
			re := globToRegexp(a.Pattern)
			var hits []string
			err = walk(ctx, root, func(abs string, d fs.DirEntry) error {
				if re.MatchString(filepath.ToSlash(mustRel(root, abs))) {
					hits = append(hits, rel(s, abs))
				}
				return nil
			})
			if err != nil {
				return "", err
			}
			sort.Strings(hits)
			if len(hits) > 500 {
				hits = append(hits[:500], fmt.Sprintf("[... %d more ...]", len(hits)-500))
			}
			if len(hits) == 0 {
				return "no matches", nil
			}
			return strings.Join(hits, "\n"), nil
		},
	}
}

func (b *Builtins) grepTool() module.Tool {
	type args struct {
		Pattern    string `json:"pattern"`
		Path       string `json:"path"`
		Glob       string `json:"glob"`
		IgnoreCase bool   `json:"ignore_case"`
		Max        int    `json:"max"`
	}
	return module.Tool{
		Name:        "grep",
		Description: "Search file contents with a Go regular expression. Output is path:line: text. glob filters file names; max caps matching lines (default 200).",
		Schema: schema(`{"type":"object","required":["pattern"],"properties":{
			"pattern":{"type":"string"},"path":{"type":"string"},"glob":{"type":"string"},
			"ignore_case":{"type":"boolean"},"max":{"type":"integer"}}}`),
		Run: func(ctx context.Context, s module.Session, raw json.RawMessage) (string, error) {
			a, err := decode[args](raw)
			if err != nil {
				return "", err
			}
			if a.Pattern == "" {
				return "", fmt.Errorf("pattern is required")
			}
			pat := a.Pattern
			if a.IgnoreCase {
				pat = "(?i)" + pat
			}
			re, err := regexp.Compile(pat)
			if err != nil {
				return "", fmt.Errorf("bad pattern: %w", err)
			}
			var nameRe *regexp.Regexp
			if a.Glob != "" {
				nameRe = globToRegexp(a.Glob)
			}
			if a.Max < 1 {
				a.Max = 200
			}
			root := s.Workspace().Path
			if a.Path != "" {
				if root, err = resolve(s, a.Path); err != nil {
					return "", err
				}
			}
			var lines []string
			err = walk(ctx, root, func(abs string, d fs.DirEntry) error {
				if nameRe != nil && !nameRe.MatchString(filepath.Base(abs)) &&
					!nameRe.MatchString(filepath.ToSlash(mustRel(root, abs))) {
					return nil
				}
				data, err := os.ReadFile(abs)
				if err != nil || isBinary(data) {
					return nil
				}
				n := 0
				for _, line := range bytes.Split(data, []byte("\n")) {
					n++
					if re.Match(line) {
						lines = append(lines, fmt.Sprintf("%s:%d: %s", rel(s, abs), n, strings.TrimRight(string(line), "\r")))
						if len(lines) >= a.Max {
							return fs.SkipAll
						}
					}
				}
				return nil
			})
			if err != nil {
				return "", err
			}
			if len(lines) == 0 {
				return "no matches", nil
			}
			return truncate(strings.Join(lines, "\n"), b.maxOutput()), nil
		},
	}
}

func mustRel(root, abs string) string {
	r, err := filepath.Rel(root, abs)
	if err != nil {
		return abs
	}
	return r
}

// walk visits regular files under root in lexical order, skipping skipDir
// entries and honouring ctx.
func walk(ctx context.Context, root string, fn func(abs string, d fs.DirEntry) error) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped, not fatal
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if p != root && skipDir[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return fn(p, d)
	})
}

// isBinary reports a NUL byte in the first 512 bytes.
func isBinary(b []byte) bool {
	if len(b) > 512 {
		b = b[:512]
	}
	return bytes.IndexByte(b, 0) >= 0
}

// globToRegexp converts a glob with *, ?, ** into an anchored regexp over
// slash-separated relative paths.
func globToRegexp(glob string) *regexp.Regexp {
	var sb strings.Builder
	sb.WriteString("^")
	g := filepath.ToSlash(glob)
	for i := 0; i < len(g); i++ {
		c := g[i]
		switch {
		case c == '*' && i+1 < len(g) && g[i+1] == '*':
			i++
			if i+1 < len(g) && g[i+1] == '/' {
				i++
				sb.WriteString("(?:.*/)?")
			} else {
				sb.WriteString(".*")
			}
		case c == '*':
			sb.WriteString("[^/]*")
		case c == '?':
			sb.WriteString("[^/]")
		default:
			sb.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	sb.WriteString("$")
	return regexp.MustCompile(sb.String())
}
