package tools

import (
	"fmt"
	"strings"
	"unicode"
)

// Limits on the nearest-match search, so a missed edit on a huge file stays cheap.
const (
	nearestMaxFileLines = 20000
	nearestMaxShown     = 40
	nearestMinScore     = 0.5
)

// missedEdit explains why old was not found in text, so the model's next
// attempt has something to aim at. A bare "not found" is what kept the model
// retrying the same edit: each retry got back exactly the same words.
//
// In order: the edit looks already applied (new is there, old is not); old
// matches some lines except for whitespace or line endings; or the closest
// region by line similarity, with line numbers. Empty when nothing is close.
func missedEdit(text, old, new string) string {
	if new != "" && strings.Contains(text, new) {
		return fmt.Sprintf("the new text is already there at line %d: this edit looks already applied",
			strings.Count(text[:strings.Index(text, new)], "\n")+1)
	}
	lines := strings.Split(text, "\n")
	if len(lines) > nearestMaxFileLines {
		return ""
	}
	want := strings.Split(strings.Trim(old, "\n"), "\n")
	start, score := nearest(lines, want)
	if start < 0 || score < nearestMinScore {
		return "no similar lines found; read the file before editing it again"
	}
	end := start + len(want)
	head := fmt.Sprintf("nearest match, lines %d-%d", start+1, end)
	if score == 1 {
		head += " (differs only in whitespace or line endings)"
	}
	var sb strings.Builder
	sb.WriteString(head + ":\n")
	for i := start; i < end && i < start+nearestMaxShown; i++ {
		fmt.Fprintf(&sb, "%d\t%s\n", i+1, strings.TrimRight(lines[i], "\r"))
	}
	if end-start > nearestMaxShown {
		fmt.Fprintf(&sb, "[... %d more lines ...]\n", end-start-nearestMaxShown)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// nearest returns the start of the window of len(want) lines that best matches
// want, and its score in [0,1]: the mean per-line similarity, where lines equal
// after trimming whitespace score 1 and others score by shared identifiers.
func nearest(lines, want []string) (int, float64) {
	if len(want) == 0 || len(want) > len(lines) {
		return -1, 0
	}
	wantTrim := make([]string, len(want))
	wantTok := make([]map[string]bool, len(want))
	for i, w := range want {
		wantTrim[i] = strings.TrimSpace(w)
		wantTok[i] = tokens(w)
	}
	fileTok := make([]map[string]bool, len(lines))
	for i, l := range lines {
		fileTok[i] = tokens(l)
	}
	best, bestScore := -1, 0.0
	for s := 0; s+len(want) <= len(lines); s++ {
		sum := 0.0
		for i := range want {
			if strings.TrimSpace(lines[s+i]) == wantTrim[i] {
				sum++
			} else {
				sum += jaccard(fileTok[s+i], wantTok[i])
			}
		}
		if sc := sum / float64(len(want)); sc > bestScore {
			best, bestScore = s, sc
		}
	}
	return best, bestScore
}

// tokens splits a line into its identifiers, numbers and punctuation runs.
func tokens(line string) map[string]bool {
	out := map[string]bool{}
	for _, f := range strings.FieldsFunc(line, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	}) {
		out[f] = true
	}
	return out
}

func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	both := 0
	for k := range a {
		if b[k] {
			both++
		}
	}
	return float64(both) / float64(len(a)+len(b)-both)
}
