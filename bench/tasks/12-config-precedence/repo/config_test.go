package fixture

import "testing"

func TestFlagsBeatDefaults(t *testing.T) {
	got := Resolve(
		Layer{"port": "8080"},
		nil, nil,
		Layer{"port": "9090"},
	)
	if got["port"] != "9090" {
		t.Errorf("port = %q, want 9090", got["port"])
	}
}
