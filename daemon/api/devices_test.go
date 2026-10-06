package api

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/corporealshift/nabu/protocol"
)

// fakeNotify records what the API tells the notifier.
type fakeNotify struct {
	mu      sync.Mutex
	devices []string
	events  []string
}

func (f *fakeNotify) Register(token, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.devices = append(f.devices, token+"/"+name)
	return nil
}

func (f *fakeNotify) Unregister(token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.devices = slices.DeleteFunc(f.devices, func(d string) bool { return len(d) > len(token) && d[:len(token)+1] == token+"/" })
	return nil
}

func (f *fakeNotify) RequestOpened(sessionID, requestID, method string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "open "+method)
}

func (f *fakeNotify) RequestClosed(sessionID, requestID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "closed")
}

func (f *fakeNotify) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.events)
}

func TestDeviceRegisterAndUnregister(t *testing.T) {
	hn := newHarness(t)
	if resp := hn.call(t, 1, "nabu.device.register", map[string]any{"token": "T1"}); resp.Error == nil ||
		resp.Error.Code != protocol.CodeInternalError {
		t.Errorf("a daemon with no notifier: %+v", resp.Error)
	}

	f := &fakeNotify{}
	hn.h.Notify = f
	var out map[string]any
	result(t, hn.call(t, 2, "nabu.device.register", map[string]any{"token": "T1", "name": "Pixel"}), &out)
	if resp := hn.call(t, 3, "nabu.device.register", map[string]any{"name": "Pixel"}); resp.Error == nil ||
		resp.Error.Code != protocol.CodeInvalidParams {
		t.Errorf("no token: %+v", resp.Error)
	}
	if !slices.Equal(f.devices, []string{"T1/Pixel"}) {
		t.Errorf("devices = %q", f.devices)
	}
	result(t, hn.call(t, 4, "nabu.device.unregister", map[string]any{"token": "T1"}), &out)
	if len(f.devices) != 0 {
		t.Errorf("after unregister: %q", f.devices)
	}
}

// A question opening tells the notifier, and so does its closing, however
// it closes: here, answered.
func TestARequestOpeningAndClosingReachesTheNotifier(t *testing.T) {
	hn := newHarness(t)
	f := &fakeNotify{}
	hn.h.Notify = f
	id := hn.mustCreate(t)
	cs, _ := hn.attach(t)
	hn.subscribeVia(t, cs, id)

	done := make(chan struct{})
	go func() {
		_, _ = hn.h.Ask(context.Background(), id, "which branch?", []string{"main"})
		close(done)
	}()
	hn.answer(t, cs, hn.pendingID(t), askReply{Answer: "main"})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Ask never returned")
	}
	if got := f.seen(); !slices.Equal(got, []string{"open nabu.rpc.ui.ask", "closed"}) {
		t.Errorf("notifier heard %q", got)
	}
}
