package github

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	if body != "" {
		if err := os.MkdirAll(Dir(root), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(ConfigPath(root), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestLoadConfig(t *testing.T) {
	const repo = `"repos":[{"name":"kyle/breezeway","clone":"C:/src/breezeway"}]`
	tests := []struct {
		name    string
		body    string
		wantErr string
		check   func(t *testing.T, c Config)
	}{
		{name: "missing file", body: "", wantErr: filepath.Join("github", "config.json")},
		{name: "no repos", body: `{}`, wantErr: "lists no repos"},
		{name: "bad name", body: `{"repos":[{"name":"breezeway","clone":"x"}]}`, wantErr: "not owner/repo"},
		{name: "nested name", body: `{"repos":[{"name":"a/b/c","clone":"x"}]}`, wantErr: "not owner/repo"},
		{name: "no clone", body: `{"repos":[{"name":"a/b"}]}`, wantErr: "no clone path"},
		{name: "bad duration", body: `{` + repo + `,"poll":"soon"}`, wantErr: `"soon"`},
		{name: "defaults", body: `{` + repo + `}`, check: func(t *testing.T, c Config) {
			if c.Label != "nabu" || time.Duration(c.Poll) != 2*time.Minute || c.QuietFor() != 5*time.Minute ||
				c.MaxJobs != 1 || !c.ReviewEnabled() || !c.ReviewPushes() || c.Review.MaxTurns != 30 ||
				!c.CommentsEnabled() || c.Comments.MaxTurns != 60 {
				t.Errorf("defaults = %+v", c)
			}
		}},
		{name: "zero quiet is kept", body: `{` + repo + `,"quiet":"0s"}`, check: func(t *testing.T, c Config) {
			if c.QuietFor() != 0 {
				t.Errorf("quiet = %v, want 0", c.QuietFor())
			}
		}},
		{name: "settings", body: `{` + repo + `,"label":"bot","poll":"30s","max_jobs":2,
			"review":{"enabled":false,"pushes":false,"max_turns":10}}`, check: func(t *testing.T, c Config) {
			if c.Label != "bot" || time.Duration(c.Poll) != 30*time.Second || c.MaxJobs != 2 ||
				c.ReviewEnabled() || c.ReviewPushes() || c.Review.MaxTurns != 10 {
				t.Errorf("settings = %+v", c)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := Load(writeConfig(t, tt.body))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to mention %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, c)
		})
	}
}

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "github", "state.json")
	empty, err := LoadState(path)
	if err != nil || len(empty.Repos) != 0 {
		t.Fatalf("missing file: %+v, %v", empty, err)
	}

	at := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	s := &State{}
	r := s.Repo("kyle/breezeway")
	r.Seen[4] = Seen{SHA: "aaa", At: at}
	r.Reviewed[3] = Reviewed{SHA: "bbb", Summary: "fine"}
	r.Failed[5] = "ccc"
	r.Running = []Job{{Kind: KindReview, PR: 4, SHA: "aaa", Base: "main", SessionID: "01X", Worktree: "w", Started: at}}
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Repo("kyle/breezeway"), r) {
		t.Errorf("round trip:\n got %+v\nwant %+v", got.Repo("kyle/breezeway"), r)
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("Save left %d files behind, want only state.json", len(entries))
	}
}

func TestStatePathKeepsDryRunsApart(t *testing.T) {
	if StatePath("r", false) == StatePath("r", true) {
		t.Error("a dry run shares the real state file")
	}
}

func TestObserve(t *testing.T) {
	t0 := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	st := (&State{}).Repo("a/b")
	Observe(st, []PR{{Number: 1, HeadSHA: "a"}, {Number: 2, HeadSHA: "x"}}, t0)
	Observe(st, []PR{{Number: 1, HeadSHA: "a"}, {Number: 2, HeadSHA: "y"}}, t0.Add(time.Minute))

	if st.Seen[1].At != t0 {
		t.Errorf("an unchanged head moved its first-seen time to %v", st.Seen[1].At)
	}
	if st.Seen[2] != (Seen{SHA: "y", At: t0.Add(time.Minute)}) {
		t.Errorf("a new head was not seen afresh: %+v", st.Seen[2])
	}
	Observe(st, []PR{{Number: 2, HeadSHA: "y"}}, t0.Add(2*time.Minute))
	if _, ok := st.Seen[1]; ok {
		t.Error("a closed PR is still remembered")
	}
}

func TestReviewJobs(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	long := now.Add(-time.Hour)
	f := false
	base := Config{}
	base.withDefaults()
	noPushes := base
	noPushes.Review.Pushes = &f
	off := base
	off.Review.Enabled = &f

	pr := PR{Number: 7, HeadSHA: "new"}
	tests := []struct {
		name  string
		cfg   Config
		pr    PR
		setup func(r *RepoState)
		want  bool
	}{
		{name: "ready", cfg: base, pr: pr, want: true},
		{name: "reviewing off", cfg: off, pr: pr},
		{name: "draft", cfg: base, pr: PR{Number: 7, HeadSHA: "new", Draft: true}},
		{name: "fork", cfg: base, pr: PR{Number: 7, HeadSHA: "new", Fork: true}},
		{name: "job running", cfg: base, pr: pr, setup: func(r *RepoState) { r.Running = []Job{{PR: 7}} }},
		{name: "head reviewed", cfg: base, pr: pr, setup: func(r *RepoState) { r.Reviewed[7] = Reviewed{SHA: "new"} }},
		{name: "new head after a review", cfg: base, pr: pr, want: true,
			setup: func(r *RepoState) { r.Reviewed[7] = Reviewed{SHA: "old"} }},
		{name: "new head, pushes off", cfg: noPushes, pr: pr,
			setup: func(r *RepoState) { r.Reviewed[7] = Reviewed{SHA: "old"} }},
		{name: "head failed", cfg: base, pr: pr, setup: func(r *RepoState) { r.Failed[7] = "new" }},
		{name: "new head after a failure", cfg: base, pr: pr, want: true,
			setup: func(r *RepoState) { r.Failed[7] = "old" }},
		{name: "inside the quiet period", cfg: base, pr: pr,
			setup: func(r *RepoState) { r.Seen[7] = Seen{SHA: "new", At: now.Add(-time.Minute)} }},
		{name: "never seen", cfg: base, pr: pr, setup: func(r *RepoState) { delete(r.Seen, 7) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := (&State{}).Repo("a/b")
			r.Seen[7] = Seen{SHA: tt.pr.HeadSHA, At: long}
			if tt.setup != nil {
				tt.setup(r)
			}
			got := ReviewJobs(tt.cfg, r, []PR{tt.pr}, now)
			if (len(got) == 1) != tt.want {
				t.Errorf("jobs = %+v, want a job: %v", got, tt.want)
			}
		})
	}
}

func TestReviewJobsOldestFirst(t *testing.T) {
	now := time.Now()
	cfg := Config{}
	cfg.withDefaults()
	r := (&State{}).Repo("a/b")
	prs := []PR{{Number: 9, HeadSHA: "c"}, {Number: 2, HeadSHA: "a"}, {Number: 5, HeadSHA: "b"}}
	Observe(r, prs, now.Add(-time.Hour))
	var got []int
	for _, p := range ReviewJobs(cfg, r, prs, now) {
		got = append(got, p.Number)
	}
	if !reflect.DeepEqual(got, []int{2, 5, 9}) {
		t.Errorf("order = %v", got)
	}
}

func TestAdmit(t *testing.T) {
	q := []int{1, 2, 3}
	tests := []struct {
		max, running int
		want         []int
	}{
		{1, 0, []int{1}},
		{1, 1, nil},
		{2, 1, []int{1}},
		{5, 0, []int{1, 2, 3}},
		{1, 3, nil},
	}
	for _, tt := range tests {
		if got := Admit(tt.max, tt.running, q); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Admit(%d, %d) = %v, want %v", tt.max, tt.running, got, tt.want)
		}
	}
}
