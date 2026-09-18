package notes

import "encoding/json"

// schema is the JSON Schema literal for a tool, kept as a helper so the tool
// definitions read as one thing.
func schema(s string) json.RawMessage { return json.RawMessage(s) }

// decode unmarshals tool arguments leniently: absent arguments are the zero
// value rather than an error, matching the built-in tools.
func decode(raw json.RawMessage, into any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, into)
}
