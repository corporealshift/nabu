package runs

import (
	"encoding/json"
	"strings"
)

// briefJSON is a brief as Claude writes one.
type briefJSON struct {
	Title string `json:"title"`
	Brief string `json:"brief"`
}

// briefsOf keeps the briefs that have both a title and a text.
func briefsOf(in []briefJSON) []GoalBrief {
	var out []GoalBrief
	for _, b := range in {
		t, text := strings.TrimSpace(b.Title), strings.TrimSpace(b.Brief)
		if t != "" && text != "" {
			out = append(out, GoalBrief{Title: t, Brief: text})
		}
	}
	return out
}

// lastJSON decodes the last fenced json block of an answer into v.
func lastJSON(answer string, v any) bool {
	m := jsonBlock.FindAllStringSubmatch(answer, -1)
	if len(m) == 0 {
		return false
	}
	return json.Unmarshal([]byte(m[len(m)-1][1]), v) == nil
}

// ParseBreakdown reads Claude's breakdown of a goal. ok is false for an
// answer with no block, a block that does not decode, or no usable brief:
// none of those can start a run.
func ParseBreakdown(answer string) (doneWhen []string, briefs []GoalBrief, ok bool) {
	var v struct {
		DoneWhen []string    `json:"done_when"`
		Briefs   []briefJSON `json:"briefs"`
	}
	if !lastJSON(answer, &v) {
		return nil, nil, false
	}
	for _, d := range v.DoneWhen {
		if d = strings.TrimSpace(d); d != "" {
			doneWhen = append(doneWhen, d)
		}
	}
	briefs = briefsOf(v.Briefs)
	if len(briefs) == 0 {
		return nil, nil, false
	}
	return doneWhen, briefs, true
}

// ParseCheck reads Claude's verdict on a goal. An unmet goal with no brief
// to go on cannot be acted on, so it is not an answer.
func ParseCheck(answer string) (met bool, reason string, briefs []GoalBrief, ok bool) {
	var v struct {
		Met    *bool       `json:"met"`
		Reason string      `json:"reason"`
		Briefs []briefJSON `json:"briefs"`
	}
	if !lastJSON(answer, &v) || v.Met == nil {
		return false, "", nil, false
	}
	reason = strings.TrimSpace(v.Reason)
	if *v.Met {
		return true, reason, nil, true
	}
	briefs = briefsOf(v.Briefs)
	if len(briefs) == 0 {
		return false, "", nil, false
	}
	return false, reason, briefs, true
}
