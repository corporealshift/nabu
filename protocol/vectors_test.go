package protocol

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestVectors runs every conformance vector under vectors/. This is the test
// the Kotlin implementation mirrors.
func TestVectors(t *testing.T) {
	vectors, err := LoadVectors("vectors")
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) == 0 {
		t.Fatal("no vectors found")
	}
	for _, v := range vectors {
		v := v
		t.Run(filepath.ToSlash(v.Path), func(t *testing.T) {
			if v.Operation.Op == "validate" {
				got, err := executeVector(v)
				if err != nil {
					t.Fatal(err)
				}
				if ok, msg := MatchValidateError(v.Expect, got); !ok {
					t.Fatalf("%s: %s", v.Name, msg)
				}
				return
			}
			if err := RunVector(v); err != nil {
				t.Fatalf("%s: %v", v.Name, err)
			}
		})
	}
}

// TestSchemaAgreesWithGo pins the JSON Schema and the Go constants together so
// neither drifts silently.
func TestSchemaAgreesWithGo(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("schema", "event.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties struct {
			Type struct {
				Enum []string `json:"enum"`
			} `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(b, &schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.Properties.Type.Enum) != len(EventTypes) {
		t.Fatalf("schema has %d event types, Go has %d", len(schema.Properties.Type.Enum), len(EventTypes))
	}
	for i, s := range schema.Properties.Type.Enum {
		if EventType(s) != EventTypes[i] {
			t.Fatalf("event type %d: schema %q, Go %q", i, s, EventTypes[i])
		}
	}

	rb, err := os.ReadFile(filepath.Join("schema", "jsonrpc.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rpc struct {
		Defs struct {
			Method struct {
				Enum []string `json:"enum"`
			} `json:"method"`
			Failure struct {
				Properties struct {
					Error struct {
						Properties struct {
							Code struct {
								Enum []int `json:"enum"`
							} `json:"code"`
						} `json:"properties"`
					} `json:"error"`
				} `json:"properties"`
			} `json:"failure"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(rb, &rpc); err != nil {
		t.Fatal(err)
	}
	if got, want := rpc.Defs.Method.Enum, Methods; len(got) != len(want) {
		t.Fatalf("schema has %d methods, Go has %d", len(got), len(want))
	} else {
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("method %d: schema %q, Go %q", i, got[i], want[i])
			}
		}
	}
	codes := rpc.Defs.Failure.Properties.Error.Properties.Code.Enum
	if len(codes) != len(ErrorNames) {
		t.Fatalf("schema has %d error codes, Go has %d", len(codes), len(ErrorNames))
	}
	for _, c := range codes {
		if _, ok := ErrorNames[c]; !ok {
			t.Fatalf("schema error code %d unknown to Go", c)
		}
	}
}
