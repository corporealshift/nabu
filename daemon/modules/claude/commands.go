package claude

import (
	"path/filepath"
	"strings"
)

// Letting Claude run the tests
// (docs/specs/2026-10-09-claude-runs-the-tests-design.md).
//
// The reviewer stays unable to change files. What it gains is the commands the
// owner names, so that a diagnosis comes from a run of the tests rather than
// from whatever result files were last left on disk. In two liftoff sessions
// all four of Claude's answers said they could not run Gradle.

// shells run whatever they are handed. As a whole prefix, or followed by a
// flag that hands them a script, they would let Claude run anything.
var shells = map[string]bool{
	"bash": true, "sh": true, "zsh": true, "dash": true, "fish": true,
	"pwsh": true, "powershell": true, "cmd": true,
	"python": true, "python3": true, "py": true, "node": true, "ruby": true, "perl": true,
}

// wrappers run the command after them, whatever it is.
var wrappers = map[string]bool{"env": true, "sudo": true, "xargs": true, "nohup": true}

// runnable says whether a configured prefix names something specific, and
// returns it tidied. "bash gradlew.sh" runs one named file; "bash", "bash -c"
// and "env go test" could run anything.
func runnable(prefix string) (string, bool) {
	fields := strings.Fields(prefix)
	if len(fields) == 0 || strings.ContainsAny(prefix, "()*") {
		return "", false
	}
	if wrappers[program(fields[0])] {
		return "", false
	}
	// Any word, not only the first: a path with a space in it, such as
	// C:/Program Files/Git/bin/bash.exe, splits into several.
	for i, f := range fields {
		if shells[program(f)] && (i == len(fields)-1 || strings.HasPrefix(fields[i+1], "-") || strings.HasPrefix(fields[i+1], "/")) {
			return "", false
		}
	}
	return strings.Join(fields, " "), true
}

// program is a word as a program name: its base name, without .exe.
func program(word string) string {
	return strings.ToLower(strings.TrimSuffix(filepath.Base(filepath.ToSlash(word)), ".exe"))
}

// commandList names the commands for a reader: `a`, `b` and `c`.
func commandList(commands []string) string {
	quoted := make([]string, len(commands))
	for i, c := range commands {
		quoted[i] = "`" + c + "`"
	}
	if len(quoted) < 2 {
		return strings.Join(quoted, "")
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " and " + quoted[len(quoted)-1]
}
