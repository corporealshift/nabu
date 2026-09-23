package loop

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// TestReplayRealLogs runs session logs through the module and reports what it
// would have done: where it would have refused, and what it would have said.
// It is how the thresholds are checked against real loops and real checks.
// Skipped unless NABU_LOOP_REPLAY names a directory of session logs, which
// is searched with its subdirectories, so archived sessions count too:
//
//	NABU_LOOP_REPLAY=~/.nabu/sessions go test ./daemon/modules/loop/ -run Replay -v
//
// The logs have no refusals in them, so a loop the module would have refused
// carries on in the replay; each refusal after the first in a run is one the
// real session would not have reached.
func TestReplayRealLogs(t *testing.T) {
	dir := os.Getenv("NABU_LOOP_REPLAY")
	if dir == "" {
		t.Skip("set NABU_LOOP_REPLAY to a directory of session logs")
	}
	var files []string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".jsonl") {
			files = append(files, p)
		}
		return nil
	})
	if len(files) == 0 {
		t.Fatalf("no logs in %s", dir)
	}
	m := loaded(t, module.Config{})
	counts := map[string]int{}
	var refused []string
	firstRefusal := map[string]bool{}
	firstNotice := map[string]bool{}
	for _, f := range files {
		log := readLog(t, f)
		id := strings.TrimSuffix(filepath.Base(f), ".jsonl")
		for i, e := range log {
			s := &fakeSession{log: log[:i]}
			switch e.Type {
			case protocol.EventMessage:
				if protocol.MustData[protocol.MessageData](e).Role != "assistant" {
					continue
				}
				blocks, _ := m.BeforeRequest(context.Background(), s)
				for _, b := range blocks {
					kind := strings.SplitN(strings.TrimPrefix(b.Content, "[nabu loop notice] "), " has ", 2)
					switch {
					case strings.Contains(b.Content, "has now run"):
						counts["notice: identical change"]++
						if !firstNotice[id+kind[0]] {
							firstNotice[id+kind[0]] = true
							t.Logf("%s %d first identical-change notice: %s", id[:10], i, b.Content)
						}
					case strings.Contains(b.Content, "did not change it"):
						counts["notice: change not landing"]++
						t.Logf("%s %d not landing: %s", id[:10], i, clip(b.Content, 900))
					case strings.Contains(b.Content, "`wait` tool"):
						counts["hint: watching"]++
						t.Logf("%s %d watching: %s", id[:10], i, kind[0])
					}
				}
			case protocol.EventToolCall:
				c := protocol.MustData[protocol.ToolCallData](e)
				counts["calls"]++
				v := m.GateTool(context.Background(), s, *c)
				if v.Decision == module.Allow {
					continue
				}
				counts["refused"]++
				if mutating(c.Tool) {
					counts["refused: write/edit"]++
				} else {
					counts["refused: anything else"]++
				}
				key := id + c.Tool + canonical(c.Arguments)
				if !firstRefusal[key] {
					firstRefusal[key] = true
					refused = append(refused, fmt.Sprintf("%s event %d at %s: %s", id[:10], i, e.Timestamp.Format("15:04:05"), clip(v.Reason, 220)))
				}
			}
		}
	}
	var keys []string
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("%-28s %d", k, counts[k])
	}
	t.Logf("first refusal of each repeated change (%d):", len(refused))
	for _, r := range refused {
		t.Log("  " + r)
	}
	if counts["refused: anything else"] > 0 {
		t.Errorf("only writes and edits may ever be refused")
	}
}

func readLog(t *testing.T, path string) []protocol.Event {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var log []protocol.Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		var e protocol.Event
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			log = append(log, e)
		}
	}
	return log
}
