package module

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/corporealshift/nabu/protocol"
)

// TaskIDs gives each task in a task.update snapshot the id it is stored under
// (spec §9.2): its own id if it has one; else the id of an earlier task with
// the same title, so a plan resent without ids does not mint new ones on
// every call; else the next free "tN". It returns a copy of incoming.
//
// It is here, beside the hooks, because a gate sees the snapshot before it is
// stored. The verify module ran each check under the id the model sent, which
// was often none, so every check was "task:" and none could be cleared.
func TaskIDs(prev, incoming []protocol.Task) ([]protocol.Task, error) {
	next := 0
	for _, t := range prev {
		next = maxSeq(next, t.ID)
	}
	seen := map[string]bool{}
	for _, t := range incoming {
		if t.ID != "" {
			if seen[t.ID] {
				return nil, fmt.Errorf("duplicate task id %q", t.ID)
			}
			seen[t.ID] = true
			next = maxSeq(next, t.ID)
		}
	}
	byTitle := map[string]string{}
	for _, t := range prev {
		if k := strings.TrimSpace(t.Title); !seen[t.ID] && byTitle[k] == "" {
			byTitle[k] = t.ID
		}
	}
	out := make([]protocol.Task, len(incoming))
	for i, t := range incoming {
		if t.ID == "" {
			k := strings.TrimSpace(t.Title)
			if id := byTitle[k]; id != "" {
				t.ID = id
				delete(byTitle, k)
			} else {
				next++
				t.ID = "t" + strconv.Itoa(next)
			}
		}
		out[i] = t
	}
	return out, nil
}

// maxSeq returns the larger of cur and the numeric suffix of an id like "t12".
func maxSeq(cur int, id string) int {
	if strings.HasPrefix(id, "t") {
		if n, err := strconv.Atoi(id[1:]); err == nil && n > cur {
			return n
		}
	}
	return cur
}
