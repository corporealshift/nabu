package github

import (
	"reflect"
	"strings"
	"testing"
)

var issue12 = Issue{Number: 12, Title: "Add a Median function", Body: "It returns a float64.", URL: "https://github.com/kyle/breezeway/issues/12"}

func issuesRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t)
	r.gh.issues = []Issue{issue12}
	r.gh.issueComments = map[int][]Comment{12: {{Kind: CommentIssue, ID: 1, Author: "kyle", Body: "Even lengths average the middle two."}}}
	return r
}

func TestALabeledIssueGetsOneRun(t *testing.T) {
	r := issuesRig(t)
	r.poll(t)
	if len(r.d.homes) != 1 {
		t.Fatalf("homes = %d", len(r.d.homes))
	}
	h := r.d.homes["H1"]
	if h.workspace != "C:/src/breezeway" || !reflect.DeepEqual(h.labels, []string{"run:requested", "issue:kyle/breezeway/12"}) {
		t.Errorf("home = %+v", h)
	}
	for _, s := range []string{"Add a Median function\n\nIt returns a float64.", "(From issue kyle/breezeway#12", "**kyle:** Even lengths average the middle two."} {
		if !strings.Contains(h.description, s) {
			t.Errorf("brief lacks %q:\n%s", s, h.description)
		}
	}
	if r.d.creates != 0 {
		t.Error("a home is never a session that runs")
	}
	r.poll(t)
	if len(r.d.homes) != 1 {
		t.Error("a second poll made a second run")
	}
}

func TestAFailedIssueRunIsAskedForAgainWhenTheIssueChanges(t *testing.T) {
	r := issuesRig(t)
	r.poll(t)
	h := r.d.homes["H1"]
	h.labels = []string{"issue:kyle/breezeway/12", "run:failed"}

	// nabu's own report of the failure changes nothing.
	r.gh.issueComments[12] = append(r.gh.issueComments[12], Comment{Kind: CommentIssue, ID: 2, Body: Signature + " stopped working on this issue\n\n" + Marker})
	r.poll(t)
	if r.d.reruns != 0 {
		t.Fatal("nabu's own comment asked for the run again")
	}

	r.gh.issueComments[12] = append(r.gh.issueComments[12], Comment{Kind: CommentIssue, ID: 3, Author: "kyle", Body: "Use sort.Ints."})
	r.poll(t)
	if r.d.reruns != 1 || !reflect.DeepEqual(h.labels, []string{"issue:kyle/breezeway/12", "run:requested"}) || !strings.Contains(h.description, "Use sort.Ints.") {
		t.Errorf("reruns %d, home %+v", r.d.reruns, h)
	}
	if strings.Contains(h.description, "stopped working") {
		t.Error("nabu's own comment is in the brief")
	}
	h.labels = []string{"issue:kyle/breezeway/12", "run:failed"}
	r.poll(t)
	if r.d.reruns != 1 {
		t.Error("an unchanged issue was asked for again")
	}
}

func TestARunningIssueRunIsLeftAlone(t *testing.T) {
	r := issuesRig(t)
	r.poll(t)
	r.d.homes["H1"].labels = []string{"issue:kyle/breezeway/12", "run:work"}
	r.gh.issueComments[12] = append(r.gh.issueComments[12], Comment{Kind: CommentIssue, ID: 3, Author: "kyle", Body: "more"})
	r.poll(t)
	if r.d.reruns != 0 {
		t.Error("a run in progress was asked for again")
	}
}

func TestIssuesCanBeTurnedOff(t *testing.T) {
	r := issuesRig(t)
	off := false
	r.w.Cfg.Issues.Enabled = &off
	r.poll(t)
	if len(r.d.homes) != 0 {
		t.Error("issues off, but a run was made")
	}
}

func TestIssueBriefIsCut(t *testing.T) {
	long := issue12
	long.Body = strings.Repeat("x", maxBrief*2)
	b := IssueBrief("kyle/breezeway", long, nil)
	if len(b) > maxBrief || !strings.HasSuffix(b, "longer than a brief may be.)") {
		t.Errorf("brief is %d bytes, ends %q", len(b), b[len(b)-40:])
	}
}

func TestIssueLabelFitsTheLabelRules(t *testing.T) {
	if got := IssueLabel("Kyle/Breezeway", 12); got != "issue:kyle/breezeway/12" {
		t.Errorf("label = %q", got)
	}
}

func TestHandedCountsIssuesGivenToTheRunner(t *testing.T) {
	r := issuesRig(t)
	r.poll(t)
	if r.w.Handed() != 1 {
		t.Errorf("handed = %d, want 1", r.w.Handed())
	}
	r.poll(t)
	if r.w.Handed() != 0 {
		t.Errorf("handed = %d on a poll that handed nothing", r.w.Handed())
	}
}
