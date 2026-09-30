package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Marker ends everything the watcher posts. Kyle and nabu post as the same
// login, and later jobs never start from a comment that carries it, or a
// review from nabu would be answered by nabu.
const Marker = "<!-- nabu -->"

// Signature opens everything the watcher posts, so a reader can tell nabu's
// words from Kyle's: they share a login, and the marker is invisible.
const Signature = "🤖 **nabu**"

// Review is what a review session's final message ends with.
type Review struct {
	Summary  string          `json:"summary"`
	Comments []ReviewFinding `json:"comments"`
}

// ReviewFinding is one comment the model wants on a line.
type ReviewFinding struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Body string `json:"body"`
}

// ReviewPost is the body of POST repos/<repo>/pulls/<n>/reviews.
type ReviewPost struct {
	CommitID string          `json:"commit_id"`
	Body     string          `json:"body"`
	Event    string          `json:"event"`
	Comments []ReviewComment `json:"comments,omitempty"`
}

// ReviewComment is a line comment in a ReviewPost.
type ReviewComment struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Side string `json:"side"`
	Body string `json:"body"`
}

var jsonBlock = regexp.MustCompile("(?s)```json[ \t]*\r?\n(.*?)\r?\n[ \t]*```")

// ParseReview decodes the last fenced json block in a final message. Only a
// block tagged json counts: an untagged block is as likely to be a quoted
// snippet of the code under review.
func ParseReview(final string) (Review, bool) {
	var r Review
	if !decodeLastBlock(final, &r) {
		return Review{}, false
	}
	return r, true
}

// decodeLastBlock decodes the last fenced json block in a message into v.
func decodeLastBlock(final string, v any) bool {
	m := jsonBlock.FindAllStringSubmatch(final, -1)
	if len(m) == 0 {
		return false
	}
	return json.Unmarshal([]byte(m[len(m)-1][1]), v) == nil
}

var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// RightLines is, for each file in a unified diff, the new-side lines a review
// comment may point at: added and context lines inside a hunk. GitHub refuses
// the whole review if one comment points anywhere else.
//
// It counts each hunk's lines down rather than trusting prefixes, so an added
// line that reads "++ x" is not mistaken for a file header.
func RightLines(diff string) map[string]map[int]bool {
	out := map[string]map[int]bool{}
	var file string
	var oldLeft, newLeft, line int
	for _, l := range strings.Split(strings.ReplaceAll(diff, "\r\n", "\n"), "\n") {
		if oldLeft > 0 || newLeft > 0 {
			switch {
			case strings.HasPrefix(l, "+"):
				mark(out, file, line)
				line++
				newLeft--
			case strings.HasPrefix(l, "-"):
				oldLeft--
			case strings.HasPrefix(l, `\`):
				// "\ No newline at end of file" belongs to the line before.
			default:
				mark(out, file, line)
				line++
				oldLeft--
				newLeft--
			}
			continue
		}
		switch {
		case strings.HasPrefix(l, "diff --git "):
			file = ""
		case strings.HasPrefix(l, "+++ "):
			file = strings.TrimPrefix(strings.TrimPrefix(l, "+++ "), "b/")
			if file == "/dev/null" {
				file = ""
			}
		case strings.HasPrefix(l, "@@ "):
			m := hunkHeader.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			oldLeft = count(m[1])
			line, _ = strconv.Atoi(m[2])
			newLeft = count(m[3])
		}
	}
	return out
}

// count reads a hunk length, which is 1 when the header leaves it out.
func count(s string) int {
	if s == "" {
		return 1
	}
	n, _ := strconv.Atoi(s)
	return n
}

func mark(out map[string]map[int]bool, file string, line int) {
	if file == "" {
		return
	}
	if out[file] == nil {
		out[file] = map[int]bool{}
	}
	out[file][line] = true
}

// BuildReview turns a session's final message into the review to post. A
// finding on a line the diff shows becomes a line comment; any other goes in
// the body, so one bad line number does not lose the rest. Without a parsed
// review the whole final message is the body.
//
// Everything is signed as nabu's, because it is posted under Kyle's login and
// GitHub will show him as the author. A line comment is signed on its own,
// since it is read in the diff apart from the review it came with.
func BuildReview(r Review, ok bool, final, diff, sha string) ReviewPost {
	post := ReviewPost{CommitID: sha, Event: "COMMENT"}
	header := fmt.Sprintf("%s reviewed `%s`", Signature, short(sha))
	if !ok {
		post.Body = header + "\n\n" + strings.TrimSpace(final) + "\n\n" + Marker
		return post
	}
	lines := RightLines(diff)
	var body strings.Builder
	body.WriteString(header)
	if s := strings.TrimSpace(r.Summary); s != "" {
		body.WriteString("\n\n" + s)
	}
	var loose []string
	for _, c := range r.Comments {
		if strings.TrimSpace(c.Body) == "" {
			continue
		}
		if lines[c.Path][c.Line] {
			post.Comments = append(post.Comments, ReviewComment{Path: c.Path, Line: c.Line, Side: "RIGHT",
				Body: Signature + ": " + c.Body + "\n\n" + Marker})
			continue
		}
		loose = append(loose, fmt.Sprintf("- `%s:%d` — %s", c.Path, c.Line, c.Body))
	}
	if len(loose) > 0 {
		body.WriteString("\n\nOn lines outside the diff:\n\n")
		body.WriteString(strings.Join(loose, "\n"))
	}
	body.WriteString("\n\n" + Marker)
	post.Body = body.String()
	return post
}

// short is a SHA's first seven characters.
func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// JSON encodes a post as written: the default encoder would turn the
// marker's angle brackets into < escapes, which GitHub decodes but a
// person reading a dry run should not have to.
func (p ReviewPost) JSON(indent bool) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if indent {
		enc.SetIndent("", "  ")
	}
	_ = enc.Encode(p)
	return bytes.TrimRight(b.Bytes(), "\n")
}
