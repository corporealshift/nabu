package protocol

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

// Vector is one conformance case (spec §9).
type Vector struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Input       []Event         `json:"input"`
	Operation   VectorOperation `json:"operation"`
	Expect      json.RawMessage `json:"expect"`
	Path        string          `json:"-"`
}

// VectorOperation selects what to run against the input log.
type VectorOperation struct {
	Op          string  `json:"op"`
	LastEventID *string `json:"last_event_id,omitempty"`
}

// LoadVectors reads every *.json under dir, recursively, sorted by path.
func LoadVectors(dir string) ([]Vector, error) {
	var out []Vector
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var v Vector
		if err := json.Unmarshal(b, &v); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		v.Path = path
		out = append(out, v)
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, err
}

// RunVector executes one vector and returns nil on match.
func RunVector(v Vector) error {
	actual, err := executeVector(v)
	if err != nil {
		return err
	}
	var expect any
	if err := json.Unmarshal(v.Expect, &expect); err != nil {
		return fmt.Errorf("bad expect: %w", err)
	}
	got := normalize(actual)
	if v.Operation.Op == "project" {
		if err := subsetMatch(expect, got, "$"); err != nil {
			return fmt.Errorf("%s\n  got: %s", err, mustJSON(got))
		}
		return nil
	}
	if !reflect.DeepEqual(expect, got) {
		return fmt.Errorf("mismatch\n  want: %s\n  got:  %s", mustJSON(expect), mustJSON(got))
	}
	return nil
}

func executeVector(v Vector) (any, error) {
	switch v.Operation.Op {
	case "validate":
		if err := ValidateLog(v.Input); err != nil {
			return map[string]any{"valid": false, "error": err.Error()}, nil
		}
		return map[string]any{"valid": true}, nil
	}
	// Every other operation requires a valid log.
	if err := ValidateLog(v.Input); err != nil {
		return nil, fmt.Errorf("input log invalid: %w", err)
	}
	switch v.Operation.Op {
	case "events_after":
		events, synced, err := EventsAfter(v.Input, v.Operation.LastEventID)
		if err != nil {
			return map[string]any{"error": err.(*RPCError).Data.Name}, nil
		}
		ids := []string{}
		for _, e := range events {
			ids = append(ids, e.ID)
		}
		return map[string]any{"event_ids": ids, "synced": synced}, nil
	case "project":
		return Project(v.Input), nil
	case "render_state":
		return map[string]any{"text": RenderCurrentState(Project(v.Input))}, nil
	case "render_vetoes":
		return map[string]any{"text": RenderVetoes(OutstandingVetoes(v.Input))}, nil
	case "assemble":
		segs := Assemble(v.Input)
		list := []any{}
		for _, s := range segs {
			m := map[string]any{"kind": string(s.Kind)}
			if s.ID != "" {
				m["id"] = s.ID
			}
			list = append(list, m)
		}
		return map[string]any{"segments": list}, nil
	}
	return nil, fmt.Errorf("unknown op %q", v.Operation.Op)
}

// validate expectations compare the error by substring, everything else by
// deep equality after a JSON round trip.
func normalize(v any) any {
	b := mustJSON(v)
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// subsetMatch checks that every field in expect appears with an equal value in
// got; objects recurse, everything else must be deeply equal.
func subsetMatch(expect, got any, path string) error {
	em, eok := expect.(map[string]any)
	gm, gok := got.(map[string]any)
	if eok && gok {
		for k, ev := range em {
			gv, ok := gm[k]
			if !ok {
				return fmt.Errorf("%s.%s: missing (want %s)", path, k, mustJSON(ev))
			}
			if err := subsetMatch(ev, gv, path+"."+k); err != nil {
				return err
			}
		}
		return nil
	}
	if !reflect.DeepEqual(expect, got) {
		return fmt.Errorf("%s: want %s, got %s", path, mustJSON(expect), mustJSON(got))
	}
	return nil
}

// MatchValidateError is used by the runner for validate vectors: the expected
// error is a substring of the actual one.
func MatchValidateError(expect json.RawMessage, got any) (bool, string) {
	var e struct {
		Valid bool   `json:"valid"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(expect, &e); err != nil {
		return false, "bad expect: " + err.Error()
	}
	g := got.(map[string]any)
	if e.Valid != g["valid"].(bool) {
		return false, fmt.Sprintf("want valid=%v, got %v (%v)", e.Valid, g["valid"], g["error"])
	}
	if !e.Valid && !strings.Contains(g["error"].(string), e.Error) {
		return false, fmt.Sprintf("want error containing %q, got %q", e.Error, g["error"])
	}
	return true, ""
}
