package fixture

import "testing"

func TestResolveContract(t *testing.T) {
	tests := []struct {
		name                       string
		defaults, file, env, flags Layer
		key, want                  string
	}{
		{"defaults alone", Layer{"a": "1"}, nil, nil, nil, "a", "1"},
		{"file beats defaults", Layer{"a": "1"}, Layer{"a": "2"}, nil, nil, "a", "2"},
		{"env beats file", Layer{"a": "1"}, Layer{"a": "2"}, Layer{"a": "3"}, nil, "a", "3"},
		{"flags beat env", Layer{"a": "1"}, nil, Layer{"a": "3"}, Layer{"a": "4"}, "a", "4"},
		// A layer that says nothing about a key must not erase the one below.
		{"a silent layer defers", Layer{"a": "1"}, Layer{"b": "x"}, nil, nil, "a", "1"},
		// An explicit empty value is a decision, not a silence.
		{"an empty value still wins", Layer{"a": "1"}, nil, nil, Layer{"a": ""}, "a", ""},
		{"zero is a value", Layer{"a": "1"}, Layer{"a": "0"}, nil, nil, "a", "0"},
		{"unmentioned key is empty", Layer{"a": "1"}, nil, nil, nil, "zz", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Resolve(tt.defaults, tt.file, tt.env, tt.flags)
			if got[tt.key] != tt.want {
				t.Errorf("%s = %q, want %q", tt.key, got[tt.key], tt.want)
			}
		})
	}
}

// Every key mentioned anywhere has to appear, or a caller ranging over the
// result silently loses settings.
func TestResolveKeepsEveryKey(t *testing.T) {
	got := Resolve(Layer{"a": "1"}, Layer{"b": "2"}, Layer{"c": "3"}, Layer{"d": "4"})
	for _, k := range []string{"a", "b", "c", "d"} {
		if _, ok := got[k]; !ok {
			t.Errorf("%q went missing", k)
		}
	}
}

// Resolving must not write into the caller's layers.
func TestResolveDoesNotMutateLayers(t *testing.T) {
	defaults := Layer{"a": "1"}
	Resolve(defaults, Layer{"a": "2"}, nil, nil)
	if defaults["a"] != "1" {
		t.Errorf("defaults were modified: %v", defaults)
	}
}
