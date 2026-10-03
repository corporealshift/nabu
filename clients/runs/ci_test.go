package runs

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	c := func(states ...string) []Check {
		var out []Check
		for _, s := range states {
			out = append(out, Check{Name: s, State: s})
		}
		return out
	}
	tests := []struct {
		checks []Check
		want   string
	}{
		{nil, CINone},
		{c("SUCCESS", "SKIPPED", "NEUTRAL"), CIPass},
		{c("success"), CIPass},
		{c("SUCCESS", "FAILURE"), CIFail},
		{c("ERROR"), CIFail},
		{c("CANCELLED"), CIFail},
		{c("TIMED_OUT"), CIFail},
		{c("ACTION_REQUIRED"), CIFail},
		{c("SUCCESS", "IN_PROGRESS"), CIPending},
		// Pending wins over a failure, so one fix sees every failure.
		{c("FAILURE", "QUEUED"), CIPending},
		{c("PENDING"), CIPending},
		{c(""), CIPending},
	}
	for _, tt := range tests {
		if got := Classify(tt.checks); got != tt.want {
			t.Errorf("Classify(%v) = %q, want %q", tt.checks, got, tt.want)
		}
	}
	if f := Failed(c("SUCCESS", "FAILURE", "ERROR")); len(f) != 2 {
		t.Errorf("Failed = %v", f)
	}
}

func TestRunID(t *testing.T) {
	for link, want := range map[string]string{
		"https://github.com/kyle/x/actions/runs/36764225332/job/110054192360": "36764225332",
		"https://github.com/kyle/x/actions/runs/42":                           "42",
		"https://ci.example.com/build/7":                                      "",
		"":                                                                    "",
	} {
		if got := RunID(link); got != want {
			t.Errorf("RunID(%q) = %q, want %q", link, got, want)
		}
	}
}

func TestPRChecksDecodesBothKinds(t *testing.T) {
	out := `{"state":"OPEN","statusCheckRollup":[
		{"__typename":"CheckRun","name":"gate (ubuntu)","status":"COMPLETED","conclusion":"FAILURE","detailsUrl":"https://github.com/k/x/actions/runs/9/job/1"},
		{"__typename":"CheckRun","name":"gate (windows)","status":"IN_PROGRESS","conclusion":"","detailsUrl":"u2"},
		{"__typename":"StatusContext","context":"ci/legacy","state":"SUCCESS","targetUrl":"u3"}]}`
	run, calls := recorder(out)
	state, checks, err := GHCLI{run}.PRChecks(context.Background(), "C:/w", 7)
	if err != nil {
		t.Fatal(err)
	}
	want := []Check{
		{Name: "gate (ubuntu)", State: "FAILURE", Link: "https://github.com/k/x/actions/runs/9/job/1"},
		{Name: "gate (windows)", State: "IN_PROGRESS", Link: "u2"},
		{Name: "ci/legacy", State: "SUCCESS", Link: "u3"},
	}
	if state != "OPEN" || !reflect.DeepEqual(checks, want) {
		t.Errorf("PRChecks = %q, %+v", state, checks)
	}
	if got := (*calls)[0]; !reflect.DeepEqual(got.args, []string{"gh", "pr", "view", "7", "--json", "state,statusCheckRollup"}) || got.dir != "C:/w" {
		t.Errorf("call = %+v", got)
	}
}

func TestFailedLog(t *testing.T) {
	run, calls := recorder(strings.Repeat("x", maxCheckOutput+500) + "THE END")
	log, err := GHCLI{run}.FailedLog(context.Background(), "C:/w", "42")
	if err != nil || !strings.HasSuffix(log, "THE END") || !strings.HasPrefix(log, "(earlier log cut)") {
		t.Errorf("log = %d bytes, %v", len(log), err)
	}
	if got := (*calls)[0].args; !reflect.DeepEqual(got, []string{"gh", "run", "view", "42", "--log-failed"}) {
		t.Errorf("args = %q", got)
	}
}

func TestCIFixPrompt(t *testing.T) {
	r := Run{Slug: "x-1234"}
	p, goal := CIFixPrompt(r, "### lint\n\ngofmt -l found stats.go")
	for _, s := range []string{"### lint", "gofmt -l found stats.go", "CI may check things", r.File(RevisionFile), "Never create, edit, rename or delete " + r.File(VerifyFile)} {
		if !strings.Contains(p, s) {
			t.Errorf("prompt lacks %q", s)
		}
	}
	if !strings.Contains(goal, "CI failures") || !strings.Contains(goal, r.File(VerifyFile)+" is untouched") {
		t.Errorf("goal = %q", goal)
	}
}
