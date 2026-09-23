// Package artifact lets the agent make a page for the person to look at: a
// chart of what it measured, a table to sort, a diagram, a small interactive
// tool (issue 41).
//
// # Where the page lives
//
// In the log, and nowhere else. The page is the argument of the tool call, so
// the call event is the artifact: every client already has it, it replays with
// the session, and a request built from the log is unchanged (invariant 3).
// Writing the same name again is a new version; clients show the latest. There
// is no file and no server to keep in step with the log.
//
// # Why the clients sandbox it
//
// The page is HTML the model wrote, and the model reads things it did not
// write: a fetched web page can steer it. Opened, the page is a program on the
// person's machine or phone, near a daemon that runs shell commands. So clients
// open it with a content security policy that allows no network access at all,
// which covers fetch and WebSocket, and the phone's WebView also refuses
// network loads. The daemon, for its part, refuses a WebSocket whose Origin is
// not its own.
package artifact

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/corporealshift/nabu/daemon/module"
	"github.com/corporealshift/nabu/protocol"
)

// Tool is the tool's name. Clients look for calls to it in the log.
const Tool = "artifact"

// maxHTML bounds one page. It is carried in the log and in the request after
// it, so a page is context the model pays for on every turn that follows.
const maxHTML = 256 << 10

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Module is the artifact module.
type Module struct {
	enabled bool
}

func (m *Module) Name() string { return "artifact" }

func (m *Module) Init(_ module.Host, cfg module.Config) error {
	m.enabled = cfg.Enabled()
	return nil
}

// Tools implements module.ToolProvider.
func (m *Module) Tools() []module.Tool {
	if !m.enabled {
		return nil
	}
	return []module.Tool{{
		Name: Tool,
		Description: "Make a page for the person to open and look at: a chart of numbers you " +
			"gathered, a table, a diagram, a small interactive tool. Use it when showing is " +
			"clearer than telling; not for code they should keep, which belongs in the repository. " +
			"The page is one self-contained HTML document: inline its CSS and JavaScript, and " +
			"draw with SVG or canvas. It cannot load anything from the network — no CDN, no " +
			"fonts, no fetch — so do not try. Writing the same name again replaces it with a " +
			"new version. Keep it small: the page stays in the conversation.",
		Schema: json.RawMessage(`{"type":"object","required":["name","title","html"],"properties":{` +
			`"name":{"type":"string","description":"short id, lowercase letters, digits and hyphens: e.g. test-timings"},` +
			`"title":{"type":"string","description":"what the page shows, in a few words"},` +
			`"html":{"type":"string","description":"the whole page: one self-contained HTML document"}}}`),
		Run: m.run,
	}}
}

// Args are the tool's arguments, which clients read back out of the log.
type Args struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	HTML  string `json:"html"`
}

func (m *Module) run(_ context.Context, s module.Session, raw json.RawMessage) (string, error) {
	var a Args
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("artifact: %w", err)
	}
	a.Name, a.Title = strings.TrimSpace(a.Name), strings.TrimSpace(a.Title)
	switch {
	case !validName.MatchString(a.Name):
		return "", fmt.Errorf("artifact: name %q must be lowercase letters, digits and hyphens, at most 64", a.Name)
	case a.Title == "":
		return "", fmt.Errorf("artifact: a title is required")
	case strings.TrimSpace(a.HTML) == "":
		return "", fmt.Errorf("artifact: the page is empty")
	case len(a.HTML) > maxHTML:
		return "", fmt.Errorf("artifact: the page is %d KB; keep it under %d KB", len(a.HTML)>>10, maxHTML>>10)
	}
	version := 1 + earlier(s, a.Name)
	return fmt.Sprintf("made %q (%s), version %d. The person can open it from the transcript; "+
		"it cannot reach the network.", a.Title, a.Name, version), nil
}

// earlier counts the versions of an artifact already made in this session.
func earlier(s module.Session, name string) int {
	if s == nil {
		return 0
	}
	log, err := s.Events(nil)
	if err != nil {
		return 0
	}
	// The call being run is already logged, so it counts itself.
	n := -1
	for _, e := range log {
		if e.Type != protocol.EventToolCall {
			continue
		}
		d := protocol.MustData[protocol.ToolCallData](e)
		if d.Tool != Tool {
			continue
		}
		var a Args
		if json.Unmarshal(d.Arguments, &a) == nil && strings.TrimSpace(a.Name) == name {
			n++
		}
	}
	return max(n, 0)
}

// Sandbox is the policy a client opens a page under: scripts and styles
// written into the page run, and nothing may be loaded or connected to.
// default-src covers connect-src, so fetch and WebSocket are refused too.
const Sandbox = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; " +
	"img-src data: blob:; font-src data:; media-src data: blob:"

// Wrap returns the page ready to open, with Sandbox in force.
//
// The policy is the first thing in the document, straight after the doctype:
// a policy placed in the page's own head would come after any script the page
// put before it, and that script would run unrestricted. Put first, the parser
// opens the head for it, and the page's own html and head tags fold into those.
// A page may add a stricter policy of its own, never a looser one: policies
// only narrow.
func Wrap(html string) string {
	meta := `<meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="` + Sandbox + `">`
	body := strings.TrimLeft(strings.TrimPrefix(html, "\ufeff"), " \t\r\n")
	if len(body) >= 9 && strings.EqualFold(body[:9], "<!doctype") {
		if end := strings.Index(body, ">"); end >= 0 {
			return body[:end+1] + meta + body[end+1:]
		}
	}
	return "<!doctype html>" + meta + body
}
