package notify

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// Device is a phone that asked to be notified.
type Device struct {
	Token      string    `json:"token"`
	Name       string    `json:"name,omitempty"`
	Registered time.Time `json:"registered"`
}

// Devices is the registered phones, kept in <root>/devices.json. A phone
// registers on every connect, so the file is never the only copy of anything
// that matters: losing it costs notifications until the next connect.
type Devices struct {
	path string
	now  func() time.Time

	mu   sync.Mutex
	list []Device
}

// DevicesFile is the file's name under the nabu root.
const DevicesFile = "devices.json"

// OpenDevices reads the devices file under root, or starts an empty list.
func OpenDevices(root string, now func() time.Time) (*Devices, error) {
	if now == nil {
		now = time.Now
	}
	d := &Devices{path: filepath.Join(root, DevicesFile), now: now}
	b, err := os.ReadFile(d.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return d, nil
	case err != nil:
		return nil, fmt.Errorf("notify: %w", err)
	}
	if err := json.Unmarshal(b, &d.list); err != nil {
		return nil, fmt.Errorf("notify: %s: %w", d.path, err)
	}
	return d, nil
}

// Register adds a device, or renames and refreshes the one with this token.
func (d *Devices) Register(token, name string) error {
	if token == "" {
		return errors.New("a device needs a token")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.list = slices.DeleteFunc(d.list, func(x Device) bool { return x.Token == token })
	d.list = append(d.list, Device{Token: token, Name: name, Registered: d.now().UTC()})
	return d.save()
}

// Unregister removes the device with this token, if there is one.
func (d *Devices) Unregister(token string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := len(d.list)
	d.list = slices.DeleteFunc(d.list, func(x Device) bool { return x.Token == token })
	if len(d.list) == n {
		return nil
	}
	return d.save()
}

// Tokens is every registered token.
func (d *Devices) Tokens() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, len(d.list))
	for i, x := range d.list {
		out[i] = x.Token
	}
	return out
}

// save writes the list through a temp file and a rename. d.mu is held.
func (d *Devices) save() error {
	b, err := json.MarshalIndent(d.list, "", "  ")
	if err != nil {
		return err
	}
	tmp := d.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("notify: %w", err)
	}
	if err := os.Rename(tmp, d.path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("notify: %w", err)
	}
	return nil
}
