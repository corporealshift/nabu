package runs

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Task is one checkbox in tasks.md.
type Task struct {
	Title string
	// Detail is the indented text under the checkbox: what the task covers
	// and how its end is known.
	Detail string
	Done   bool
	// line is where the checkbox is, for ticking it.
	line int
}

var checkbox = regexp.MustCompile(`^(\s*[-*]\s+)\[([ xX])\](\s+)(.*)$`)

// ParseTasks reads the checkboxes in tasks.md, with the indented lines under
// each as its detail. Anything else in the file is ignored.
func ParseTasks(md string) []Task {
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	var out []Task
	for i, l := range lines {
		if m := checkbox.FindStringSubmatch(l); m != nil {
			out = append(out, Task{Title: strings.TrimSpace(m[4]), Done: m[2] != " ", line: i})
			continue
		}
		if len(out) == 0 {
			continue
		}
		last := &out[len(out)-1]
		switch {
		case strings.TrimSpace(l) == "":
		case l[0] == ' ' || l[0] == '\t':
			last.Detail = strings.TrimSpace(last.Detail + "\n" + strings.TrimSpace(l))
		}
	}
	return out
}

// NextTask is the first task not yet done, and how many are left.
func NextTask(tasks []Task) (int, int) {
	next, left := -1, 0
	for i, t := range tasks {
		if !t.Done {
			left++
			if next < 0 {
				next = i
			}
		}
	}
	return next, left
}

// TickTask marks task i done in tasks.md.
func TickTask(md string, i int) string {
	tasks := ParseTasks(md)
	if i < 0 || i >= len(tasks) {
		return md
	}
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	m := checkbox.FindStringSubmatch(lines[tasks[i].line])
	lines[tasks[i].line] = m[1] + "[x]" + m[3] + m[4]
	return strings.Join(lines, "\n")
}

// Blocker is one thing the final review says must be fixed before the PR.
type Blocker struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// AppendTasks adds the final review's blockers to tasks.md as tasks.
func AppendTasks(md string, blockers []Blocker) string {
	var b strings.Builder
	b.WriteString(strings.TrimRight(md, "\n"))
	b.WriteString("\n\n## Blockers from the final review\n\n")
	for _, bl := range blockers {
		fmt.Fprintf(&b, "- [ ] %s\n", strings.TrimSpace(bl.Title))
		for _, l := range strings.Split(strings.TrimSpace(bl.Detail), "\n") {
			if strings.TrimSpace(l) != "" {
				fmt.Fprintf(&b, "  %s\n", strings.TrimSpace(l))
			}
		}
	}
	return b.String()
}

// Begin and End mark a whole file in one of Claude's answers. Markers rather
// than a code fence, because a plan is markdown and carries fences of its own.
func Begin(name string) string { return "=====BEGIN " + name + "=====" }
func End(name string) string   { return "=====END " + name + "=====" }

// ParseRewrite reads an answer that either replaces a file or says it may
// stand. text and changed are the replacement; ok is false when the answer
// does neither, which is not an answer.
func ParseRewrite(answer, name, sentinel string) (text string, changed, ok bool) {
	answer = strings.ReplaceAll(answer, "\r\n", "\n")
	if i := strings.LastIndex(answer, Begin(name)); i >= 0 {
		rest := answer[i+len(Begin(name)):]
		if j := strings.Index(rest, End(name)); j >= 0 {
			return strings.Trim(rest[:j], "\n") + "\n", true, true
		}
	}
	for _, l := range strings.Split(answer, "\n") {
		if strings.HasPrefix(strings.TrimSpace(strings.Trim(l, "*_`")), sentinel) {
			return "", false, true
		}
	}
	return "", false, false
}

// Refusal is what Claude said after REFUSED, to pass to the next fix.
func Refusal(answer string) string {
	_, after, found := strings.Cut(answer, "REFUSED")
	if !found {
		return ""
	}
	return strings.TrimSpace(strings.TrimLeft(after, ":.-— \n"))
}

var jsonBlock = regexp.MustCompile("(?s)```json[ \t]*\r?\n(.*?)\r?\n[ \t]*```")

// ParseFinal reads the final review's verdict.
func ParseFinal(answer string) (blockers []Blocker, notes []string, ok bool) {
	m := jsonBlock.FindAllStringSubmatch(answer, -1)
	if len(m) == 0 {
		return nil, nil, false
	}
	var v struct {
		Blockers []Blocker `json:"blockers"`
		Notes    []string  `json:"notes"`
	}
	if json.Unmarshal([]byte(m[len(m)-1][1]), &v) != nil {
		return nil, nil, false
	}
	for _, b := range v.Blockers {
		if strings.TrimSpace(b.Title) != "" {
			blockers = append(blockers, b)
		}
	}
	return blockers, v.Notes, true
}

var notSlug = regexp.MustCompile(`[^a-z0-9]+`)

// maxSlug is how long a slug's words may run, before its id.
const maxSlug = 40

// Slug names a run's branch and directory: the start of its brief, lower
// case and dashed, then the end of the home's id so two runs never share a
// branch.
func Slug(text, home string) string {
	s := strings.Trim(notSlug.ReplaceAllString(strings.ToLower(text), "-"), "-")
	if len(s) > maxSlug {
		s = s[:maxSlug]
		if i := strings.LastIndex(s, "-"); i > maxSlug/2 {
			s = s[:i]
		}
		s = strings.Trim(s, "-")
	}
	if s == "" {
		s = "run"
	}
	id := strings.ToLower(home)
	if len(id) > 4 {
		id = id[len(id)-4:]
	}
	return s + "-" + id
}

// DecisionsSection is the body of the plan's decisions section, without its
// heading: everything after the DecisionsHeading line up to the next heading
// of the same or a higher level. A heading inside a fenced block does not end
// it. found is false when the plan has no such section.
func DecisionsSection(plan string) (text string, found bool) {
	lines := strings.Split(strings.ReplaceAll(plan, "\r\n", "\n"), "\n")
	start := -1
	for i, l := range lines {
		if strings.EqualFold(strings.TrimSpace(l), DecisionsHeading) {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return "", false
	}
	end, fenced := len(lines), false
	for i := start; i < len(lines); i++ {
		l := lines[i]
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			fenced = !fenced
		}
		if !fenced && (strings.HasPrefix(l, "# ") || strings.HasPrefix(l, "## ")) {
			end = i
			break
		}
	}
	return strings.TrimSpace(strings.Join(lines[start:end], "\n")), true
}

// keepDecisions puts the plan's decisions back when a review's rewrite left
// the section out. The runner never judges the entries; it only keeps them
// from disappearing before the owner sees them.
func keepDecisions(plan, rewrite string) string {
	section, _ := DecisionsSection(plan)
	if section == "" {
		return rewrite
	}
	if _, has := DecisionsSection(rewrite); has {
		return rewrite
	}
	return strings.TrimRight(rewrite, "\n") + "\n\n" + DecisionsHeading +
		"\n\n_The review's rewrite left this section out; these are the plan's decisions._\n\n" + section + "\n"
}

// maxDecisions is how much of the decisions section a pull request carries.
// GitHub refuses a body over 65,536 characters.
const maxDecisions = 20000

// decisionsForPR is the pull request's account of what the run chose where
// the brief left a behavior open, from the plan at planPath.
func decisionsForPR(plan, planPath, verifyPath string) string {
	section, _ := DecisionsSection(plan)
	var b strings.Builder
	b.WriteString("\n## Decisions this run made\n\n")
	if section == "" {
		b.WriteString("The plan recorded no open decisions.\n")
		return b.String()
	}
	if len(section) > maxDecisions {
		cut := section[:maxDecisions]
		if i := strings.LastIndex(cut, "\n"); i > 0 {
			cut = cut[:i]
		}
		section = strings.ToValidUTF8(cut, "") + fmt.Sprintf("\n\n_Cut here; the rest is in `%s`._", planPath)
	}
	fmt.Fprintf(&b, "%s\n\nWhere `%s` tests one, the test's comment names it. To change one, say so in a comment on this PR.\n", section, verifyPath)
	return b.String()
}
