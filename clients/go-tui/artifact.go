package tui

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/corporealshift/nabu/daemon/modules/artifact"
	"github.com/corporealshift/nabu/protocol"
)

// rememberArtifact records a page the agent made, from its tool call. The call
// is the artifact (issue 41): there is nothing to fetch.
func (m *model) rememberArtifact(ev protocol.Event) {
	var d protocol.ToolCallData
	if unmarshal(ev, &d) != nil || d.Tool != artifact.Tool {
		return
	}
	var a artifact.Args
	if json.Unmarshal(d.Arguments, &a) != nil || a.Name == "" {
		return
	}
	if m.artifacts == nil {
		m.artifacts = map[string]artifact.Args{}
	}
	m.artifacts[a.Name] = a // a later version replaces an earlier one
	m.lastArtifact = a.Name
}

// openInBrowser hands a file to whatever the system opens web pages with. A
// variable so tests can see what would have been opened without opening it.
var openInBrowser = func(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Start()
}

// openArtifact writes the page with the sandbox in force and opens it. It is a
// command, not work done in Update: writing a file and starting a browser are
// effects, and Update stays pure.
func openArtifact(sessionID string, a artifact.Args) tea.Cmd {
	return func() tea.Msg {
		dir := filepath.Join(os.TempDir(), "nabu-artifacts", sessionID)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return errMsg{text: "could not save the page: " + err.Error()}
		}
		path := filepath.Join(dir, a.Name+".html")
		if err := os.WriteFile(path, []byte(artifact.Wrap(a.HTML)), 0o600); err != nil {
			return errMsg{text: "could not save the page: " + err.Error()}
		}
		if err := openInBrowser(path); err != nil {
			return errMsg{text: "could not open a browser (" + err.Error() + "); the page is at " + path}
		}
		return noteMsg{text: "opened " + a.Title + " — " + path}
	}
}
