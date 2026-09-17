package bench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Run is one harness's single attempt at one task.
type Run struct {
	Task    string   `json:"task"`
	Harness string   `json:"harness"`
	Repeat  int      `json:"repeat"`
	Outcome Outcome  `json:"outcome"`
	Broke   []string `json:"broke,omitempty"`
	Changed []string `json:"changed,omitempty"`
	Cost    Cost     `json:"cost"`
	Craft   Craft    `json:"craft"`
	Note    string   `json:"note,omitempty"`
	// Tail is the last of what the harness printed, kept for runs that need
	// explaining. It is the difference between a number and a diagnosis.
	Tail     string `json:"tail,omitempty"`
	Finished string `json:"finished"`
}

// Results is everything one invocation measured, and enough about how it was
// measured to know whether a later run is comparable.
type Results struct {
	Started  string   `json:"started"`
	Repeat   int      `json:"repeat"`
	Judge    string   `json:"judge"`
	Models   Models   `json:"models"`
	Tasks    []string `json:"tasks"`
	Harness  []string `json:"harnesses"`
	Runs     []Run    `json:"runs"`
	Suspects []string `json:"suspect_tasks,omitempty"`
	// Tiers is each task's tier, so a saved run can be read back without the
	// fixtures it was run against.
	Tiers map[string]string `json:"tiers,omitempty"`
}

// Models records what each harness ran on. A comparison between runs on
// different models is not a comparison of harnesses.
type Models struct {
	Local  string `json:"local"`
	Claude string `json:"claude"`
}

// Save writes the results under dir with a sortable name, and returns the path.
func (r Results) Save(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := "bench-" + time.Now().UTC().Format("20060102-150405") + ".json"
	path := filepath.Join(dir, name)

	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// LoadResults reads a saved run, for comparison.
func LoadResults(path string) (Results, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Results{}, fmt.Errorf("reading results: %w", err)
	}
	var r Results
	if err := json.Unmarshal(raw, &r); err != nil {
		return Results{}, fmt.Errorf("parsing results: %w", err)
	}
	return r, nil
}

// Tally is how one harness did on one task across its repeats.
type Tally struct {
	Task     string
	Harness  string
	Passed   int
	Attempts int
	Median   time.Duration
	Craft    float64 // mean of the three axes, over scored runs; 0 when none
	Suspect  bool
}

// PassRate is what the headline shows.
func (t Tally) PassRate() float64 {
	if t.Attempts == 0 {
		return 0
	}
	return float64(t.Passed) / float64(t.Attempts)
}

// Tally groups the runs by task and harness, in a stable order.
func (r Results) Tally() []Tally {
	type key struct{ task, harness string }
	grouped := map[key][]Run{}
	for _, run := range r.Runs {
		k := key{run.Task, run.Harness}
		grouped[k] = append(grouped[k], run)
	}

	suspect := map[string]bool{}
	for _, s := range r.Suspects {
		suspect[s] = true
	}

	var out []Tally
	for k, runs := range grouped {
		t := Tally{Task: k.task, Harness: k.harness, Suspect: suspect[k.task]}

		var durations []time.Duration
		var craftSum float64
		var craftN int
		for _, run := range runs {
			t.Attempts++
			if run.Outcome == Passed {
				t.Passed++
			}
			durations = append(durations, run.Cost.Duration)
			if run.Craft.Scored {
				craftSum += float64(run.Craft.Minimal+run.Craft.Tested+run.Craft.Focused) / 3
				craftN++
			}
		}
		t.Median = median(durations)
		if craftN > 0 {
			t.Craft = craftSum / float64(craftN)
		}
		out = append(out, t)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Task != out[j].Task {
			return out[i].Task < out[j].Task
		}
		return out[i].Harness < out[j].Harness
	})
	return out
}

func median(d []time.Duration) time.Duration {
	if len(d) == 0 {
		return 0
	}
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	return d[len(d)/2]
}

// Suspect tasks are ones the reference harness could not do. The reference is
// expected to pass everything, so its failure is evidence about the task
// rather than about the harness, and scoring it would drag all three down.
func suspectTasks(runs []Run, reference string) []string {
	attempted := map[string]bool{}
	passed := map[string]bool{}
	for _, r := range runs {
		if r.Harness != reference {
			continue
		}
		// A reference that could not run says nothing about the task. Counting
		// it would let a rate limit quietly delete tasks from the headline.
		switch r.Outcome {
		case Errored, TimedOut, Unusable:
			continue
		}
		attempted[r.Task] = true
		if r.Outcome == Passed {
			passed[r.Task] = true
		}
	}

	var out []string
	for task := range attempted {
		if !passed[task] {
			out = append(out, task)
		}
	}
	sort.Strings(out)
	return out
}
