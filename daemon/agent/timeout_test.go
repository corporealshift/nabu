package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/corporealshift/rho/daemon/module"
	"github.com/corporealshift/rho/daemon/provider"
	"github.com/corporealshift/rho/daemon/session"
	"github.com/corporealshift/rho/daemon/tools"
	"github.com/corporealshift/rho/protocol"
)

// A model call that runs past the provider's timeout ends the session with a
// notice naming the limit and the key that raises it, so nobody has to read
// the source to learn there is one.
func TestAProviderTimeoutNoticeNamesTheLimitAndKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"thinking it over\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()

	dir := t.TempDir()
	store, err := session.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	pcfg := provider.Config{Name: "strata", BaseURL: srv.URL + "/v1", ContextWindow: 8000, Timeout: 200 * time.Millisecond}
	pr := provider.NewRegistry()
	pr.Add(pcfg, provider.NewOpenAI(pcfg, srv.Client()), true)
	builtins := &tools.Builtins{}
	mr := module.NewRegistry([]module.Module{builtins}, module.Options{Log: testLogger()})
	m, err := New(Deps{Store: store, Providers: pr, Modules: mr, Builtins: builtins, Root: dir, Log: testLogger()},
		Config{DefaultModel: "strata/m"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Shutdown(context.Background()) })

	s, err := m.Create(context.Background(), dir, CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Prompt(context.Background(), s.ID(), "plan it"); err != nil {
		t.Fatal(err)
	}
	m.WaitIdle(s.ID())

	var notice string
	for _, e := range s.Events() {
		if e.Type != protocol.EventNotice {
			continue
		}
		if d := protocol.MustData[protocol.NoticeData](e); d.Level == "error" {
			notice = d.Message
		}
	}
	for _, want := range []string{"200ms provider timeout", "providers.strata.timeout"} {
		if !strings.Contains(notice, want) {
			t.Errorf("error notice %q should contain %q", notice, want)
		}
	}
	if s.State().State != protocol.StateError {
		t.Errorf("state: %s, want error as before", s.State().State)
	}
}
