package notify

import (
	"slices"
	"testing"
	"time"
)

func TestDevicesRegisterAndUnregister(t *testing.T) {
	root := t.TempDir()
	now := func() time.Time { return time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC) }
	d, err := OpenDevices(root, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Register("", "Pixel"); err == nil {
		t.Error("a device with no token was registered")
	}
	for _, tok := range []string{"A", "B", "A"} {
		if err := d.Register(tok, "Pixel"); err != nil {
			t.Fatal(err)
		}
	}
	if got := d.Tokens(); !slices.Equal(got, []string{"B", "A"}) {
		t.Errorf("tokens = %q; registering again should replace, not add", got)
	}

	// A second open reads what the first wrote.
	again, err := OpenDevices(root, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Tokens(); !slices.Equal(got, []string{"B", "A"}) {
		t.Errorf("reopened tokens = %q", got)
	}
	if err := again.Unregister("B"); err != nil {
		t.Fatal(err)
	}
	if err := again.Unregister("nobody"); err != nil {
		t.Fatal(err)
	}
	if got := again.Tokens(); !slices.Equal(got, []string{"A"}) {
		t.Errorf("after unregistering B: %q", got)
	}
}
