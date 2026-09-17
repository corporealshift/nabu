package bench

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// Report writes the human-readable summary. Three columns because the score is
// three things: a composite would hide which of them moved.
func Report(w io.Writer, r Results) {
	tallies := r.Tally()
	harnesses := r.Harness
	if len(harnesses) == 0 {
		harnesses = harnessesIn(tallies)
	}

	fmt.Fprintf(w, "\n%s\n", strings.Repeat("─", 72))
	fmt.Fprintf(w, "nabu bench · %s · %d repeat(s) · judge %s\n", r.Started, r.Repeat, r.Judge)
	if r.Models.Local != "" {
		fmt.Fprintf(w, "local %s · claude %s\n", r.Models.Local, r.Models.Claude)
	}
	fmt.Fprintf(w, "%s\n\n", strings.Repeat("─", 72))

	fmt.Fprintf(w, "%-24s", "task")
	for _, h := range harnesses {
		fmt.Fprintf(w, "%-16s", h)
	}
	fmt.Fprintln(w)

	byTask := map[string]map[string]Tally{}
	for _, t := range tallies {
		if byTask[t.Task] == nil {
			byTask[t.Task] = map[string]Tally{}
		}
		byTask[t.Task][t.Harness] = t
	}

	for _, task := range sortedKeys(byTask) {
		label := task
		if isSuspect(r, task) {
			label += " ?"
		}
		fmt.Fprintf(w, "%-24s", clipLeft(label, 23))
		for _, h := range harnesses {
			t := byTask[task][h]
			fmt.Fprintf(w, "%-16s", fmt.Sprintf("%d/%d  %4.0fs", t.Passed, t.Attempts, t.Median.Seconds()))
		}
		fmt.Fprintln(w)
	}

	fmt.Fprintln(w)
	// Per tier, because a tier every harness passes cannot show movement.
	// Basic says a harness works at all; hard is what separates them once
	// they all do.
	for _, tier := range tiersIn(r) {
		fmt.Fprintf(w, "%-24s", "PASS RATE "+tier)
		for _, h := range harnesses {
			passed, attempts := 0, 0
			for _, t := range tallies {
				if t.Harness != h || t.Suspect || r.Tiers[t.Task] != tier {
					continue
				}
				passed += t.Passed
				attempts += t.Attempts
			}
			cell := "–"
			if attempts > 0 {
				cell = fmt.Sprintf("%d/%d", passed, attempts)
			}
			fmt.Fprintf(w, "%-16s", cell)
		}
		fmt.Fprintln(w)
	}

	fmt.Fprintf(w, "%-24s", "TIME (median)")
	for _, h := range harnesses {
		var secs []float64
		for _, run := range r.Runs {
			if run.Harness == h && run.Outcome == Passed {
				secs = append(secs, run.Cost.Duration.Seconds())
			}
		}
		cell := "–"
		if len(secs) > 0 {
			cell = fmt.Sprintf("%.0fs", medianOf(secs))
		}
		fmt.Fprintf(w, "%-16s", cell)
	}
	fmt.Fprintln(w)

	fmt.Fprintf(w, "%-24s", "TURNS (median)")
	for _, h := range harnesses {
		var turns []float64
		for _, run := range r.Runs {
			if run.Harness == h && run.Outcome == Passed && run.Cost.Turns > 0 {
				turns = append(turns, float64(run.Cost.Turns))
			}
		}
		cell := "–"
		if len(turns) > 0 {
			cell = fmt.Sprintf("%.0f", medianOf(turns))
		}
		fmt.Fprintf(w, "%-16s", cell)
	}
	fmt.Fprintln(w)

	fmt.Fprintf(w, "%-24s", "CRAFT (of 5)")
	for _, h := range harnesses {
		var sum float64
		var n int
		for _, t := range tallies {
			if t.Harness == h && t.Craft > 0 && !t.Suspect {
				sum += t.Craft
				n++
			}
		}
		cell := "–"
		if n > 0 {
			cell = fmt.Sprintf("%.1f", sum/float64(n))
			if selfJudged(r, h) {
				cell += " (self)"
			}
		}
		fmt.Fprintf(w, "%-16s", cell)
	}
	fmt.Fprintln(w)

	if spend := totalUSD(r); spend > 0 {
		fmt.Fprintf(w, "%-24s$%.2f\n", "CLAUDE SPEND", spend)
	}

	if !ranReference(r) {
		fmt.Fprintf(w, "\nno reference harness in this run, so no task was checked for being broken\n")
	}
	if len(r.Suspects) > 0 {
		fmt.Fprintf(w, "\n? suspect, excluded from the rates: %s\n", strings.Join(r.Suspects, ", "))
		fmt.Fprintf(w, "  the reference harness failed these, so they are more likely broken than hard.\n")
	}

	reportProblems(w, r)
}

// reportProblems names runs that did not simply pass or fail, because those
// are usually a fault in the setup rather than a result.
func reportProblems(w io.Writer, r Results) {
	var lines []string
	for _, run := range r.Runs {
		switch run.Outcome {
		case Passed, Failed:
			continue
		}
		line := fmt.Sprintf("  %s %s: %s", run.Task, run.Harness, run.Outcome)
		if len(run.Broke) > 0 {
			line += " (" + strings.Join(run.Broke, ", ") + ")"
		}
		if run.Note != "" {
			line += " — " + firstLine(run.Note)
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(w, "\nnot a plain pass or fail:\n")
	for _, l := range dedupe(lines) {
		fmt.Fprintln(w, l)
	}
}

// Compare reports movement since an earlier run: the point of the tool is the
// gap and whether it is closing, which one run alone cannot show.
func Compare(w io.Writer, old, now Results) {
	fmt.Fprintf(w, "\nsince %s\n\n", old.Started)

	if old.Models != now.Models {
		fmt.Fprintf(w, "  models differ between the runs — this compares stacks, not harnesses\n")
		fmt.Fprintf(w, "  was: local %s, claude %s\n\n", old.Models.Local, old.Models.Claude)
	}

	before := rateIndex(old)
	after := rateIndex(now)

	keys := map[string]bool{}
	for k := range before {
		keys[k] = true
	}
	for k := range after {
		keys[k] = true
	}

	var moved int
	for _, k := range sortedKeys(keys) {
		was, is := before[k], after[k]
		if was == is {
			continue
		}
		moved++
		fmt.Fprintf(w, "  %-32s %.0f%% → %.0f%%  %s\n", k, was*100, is*100, arrow(is-was))
	}
	if moved == 0 {
		fmt.Fprintln(w, "  nothing moved")
	}
}

func arrow(delta float64) string {
	switch {
	case delta > 0:
		return "better"
	case delta < 0:
		return "worse"
	default:
		return ""
	}
}

func rateIndex(r Results) map[string]float64 {
	out := map[string]float64{}
	for _, t := range r.Tally() {
		out[t.Harness+" · "+t.Task] = t.PassRate()
	}
	return out
}

// medianOf is the middle of a slice of numbers, which says more about a
// harness than a mean does when one run went badly.
func medianOf(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sorted := append([]float64(nil), xs...)
	sort.Float64s(sorted)
	return sorted[len(sorted)/2]
}

// tiersIn lists the tiers present, basic first: it is the floor, and a hard
// score read without knowing the floor holds is misleading.
func tiersIn(r Results) []string {
	seen := map[string]bool{}
	for _, t := range r.Tiers {
		seen[t] = true
	}
	var out []string
	for _, tier := range []string{"basic", "hard"} {
		if seen[tier] {
			out = append(out, tier)
		}
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

// ranReference reports whether the harness that vouches for the tasks took
// part. Without it a rate is still a rate, but nothing has vouched for the
// tasks behind it.
func ranReference(r Results) bool {
	for _, h := range r.Harness {
		if h == "claude" {
			return true
		}
	}
	return false
}

func isSuspect(r Results, task string) bool {
	for _, s := range r.Suspects {
		if s == task {
			return true
		}
	}
	return false
}

func selfJudged(r Results, harness string) bool {
	for _, run := range r.Runs {
		if run.Harness == harness && run.Craft.SelfJudged {
			return true
		}
	}
	return false
}

func totalUSD(r Results) float64 {
	var sum float64
	for _, run := range r.Runs {
		sum += run.Cost.USD
	}
	return sum
}

func harnessesIn(tallies []Tally) []string {
	seen := map[string]bool{}
	for _, t := range tallies {
		seen[t.Harness] = true
	}
	return sortedKeys(seen)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func dedupe(lines []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range lines {
		if !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	return out
}

func clipLeft(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
