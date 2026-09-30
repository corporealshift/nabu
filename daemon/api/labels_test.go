package api

import (
	"reflect"
	"strings"
	"testing"

	"github.com/corporealshift/nabu/protocol"
)

// listed is the one summary for a session in nabu.session.list.
func (hn *harness) listed(t *testing.T, id string) map[string]any {
	t.Helper()
	var out struct {
		Sessions []map[string]any `json:"sessions"`
	}
	result(t, hn.call(t, 90, "nabu.session.list", map[string]any{}), &out)
	for _, s := range out.Sessions {
		if s["session_id"] == id {
			return s
		}
	}
	t.Fatalf("session %s not listed", id)
	return nil
}

func rpcCode(t *testing.T, resp *jsonrpcResponse) int {
	t.Helper()
	if resp == nil || resp.Error == nil {
		t.Fatal("want an rpc error, got success")
	}
	return resp.Error.Code
}

func TestCreateWithAParentAndLabels(t *testing.T) {
	hn := newHarness(t)
	home := hn.mustCreate(t)
	var out struct {
		SessionID string         `json:"session_id"`
		Event     protocol.Event `json:"event"`
	}
	result(t, hn.call(t, 2, "nabu.session.create", map[string]any{"workspace": hn.dir,
		"options": map[string]any{"parent": home, "labels": []string{"run:plan"}}}), &out)

	d := protocol.MustData[protocol.SessionData](out.Event)
	if d.Options.Parent != home || !reflect.DeepEqual(d.Options.Labels, []string{"run:plan"}) {
		t.Errorf("session event options = %+v", d.Options)
	}
	s, _ := hn.h.getSession(out.SessionID)
	if st := s.State(); st.Options.Parent != home || !reflect.DeepEqual(st.Options.Labels, []string{"run:plan"}) {
		t.Errorf("state options = %+v", st.Options)
	}
	sum := hn.listed(t, out.SessionID)
	if sum["parent"] != home || !reflect.DeepEqual(sum["labels"], []any{"run:plan"}) {
		t.Errorf("summary = %+v", sum)
	}
	if _, ok := hn.listed(t, home)["parent"]; ok {
		t.Error("a session without a parent lists one")
	}
}

func TestCreateRefusesAnUnknownParentOrBadLabels(t *testing.T) {
	hn := newHarness(t)
	before, _ := hn.store.List()
	for name, opts := range map[string]map[string]any{
		"unknown parent":   {"parent": "01JPARENT00000000000000001"},
		"not an id":        {"parent": "home"},
		"a capital":        {"labels": []string{"Run:plan"}},
		"seventeen labels": {"labels": []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q"}},
	} {
		t.Run(name, func(t *testing.T) {
			resp := hn.call(t, 1, "nabu.session.create", map[string]any{"workspace": hn.dir, "options": opts})
			if code := rpcCode(t, resp); code != protocol.CodeInvalidParams {
				t.Errorf("code = %d, want invalid params", code)
			}
		})
	}
	after, _ := hn.store.List()
	if len(after) != len(before) {
		t.Errorf("a refused create left %d sessions behind", len(after)-len(before))
	}
}

func TestAnArchivedParentIsStillAParent(t *testing.T) {
	hn := newHarness(t)
	home := hn.mustCreate(t)
	result(t, hn.call(t, 2, "nabu.session.archive", map[string]any{"session_id": home}), &struct{}{})
	resp := hn.call(t, 3, "nabu.session.create", map[string]any{"workspace": hn.dir,
		"options": map[string]any{"parent": home}})
	if resp.Error != nil {
		t.Fatalf("an archived parent was refused: %+v", resp.Error)
	}
}

func TestSetOptionLabels(t *testing.T) {
	hn := newHarness(t)
	id := hn.mustCreate(t)
	var out struct {
		EventID string `json:"event_id"`
	}
	result(t, hn.call(t, 2, "nabu.session.set_option",
		map[string]any{"session_id": id, "key": "labels", "value": []string{"run:requested"}}), &out)
	result(t, hn.call(t, 3, "nabu.session.set_option",
		map[string]any{"session_id": id, "key": "labels", "value": []string{"run:plan", "run:attempt:1/10"}}), &out)

	s, _ := hn.h.getSession(id)
	if got := s.State().Options.Labels; !reflect.DeepEqual(got, []string{"run:plan", "run:attempt:1/10"}) {
		t.Errorf("labels = %q", got)
	}
	evs := s.Events()
	last := protocol.MustData[protocol.OptionsChangeData](evs[len(evs)-1])
	if last.Key != "labels" || string(last.From) != `["run:requested"]` {
		t.Errorf("options_change = key %q from %s", last.Key, last.From)
	}
	if got := hn.listed(t, id)["labels"]; !reflect.DeepEqual(got, []any{"run:plan", "run:attempt:1/10"}) {
		t.Errorf("listed labels = %v", got)
	}

	result(t, hn.call(t, 4, "nabu.session.set_option",
		map[string]any{"session_id": id, "key": "labels", "value": []string{}}), &out)
	if got := s.State().Options.Labels; len(got) != 0 {
		t.Errorf("clearing left labels %q", got)
	}
}

func TestSetOptionRefusesParentAndBadLabels(t *testing.T) {
	hn := newHarness(t)
	id := hn.mustCreate(t)
	s, _ := hn.h.getSession(id)
	before := len(s.Events())
	for name, value := range map[string]any{
		"parent":      nil,
		"not a list":  "run:plan",
		"not strings": []any{"run:plan", 3},
		"a space":     []string{"run plan"},
		"too long":    []string{strings.Repeat("a", 65)},
	} {
		t.Run(name, func(t *testing.T) {
			key := "labels"
			if name == "parent" {
				key, value = "parent", id
			}
			resp := hn.call(t, 1, "nabu.session.set_option", map[string]any{"session_id": id, "key": key, "value": value})
			if code := rpcCode(t, resp); code != protocol.CodeInvalidParams {
				t.Errorf("code = %d, want invalid params", code)
			}
		})
	}
	if after := len(s.Events()); after != before {
		t.Errorf("refused changes appended %d events", after-before)
	}
}
