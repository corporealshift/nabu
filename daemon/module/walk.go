package module

import "strings"

// noiseDir are directories that hold generated or vendored content: nothing a
// reader wrote, and nothing worth walking.
//
// Shared because three copies of this had already drifted apart. The file tools
// descended into build/, target/ and vendor/ while the watch module did not, so
// the model could be told a file changed in a directory its own grep would
// never have shown it.
var noiseDir = map[string]bool{
	".git": true, "node_modules": true, "build": true, "target": true,
	"vendor": true, "dist": true, "__pycache__": true,
}

// NoiseDir reports whether a directory holds generated or vendored content and
// should be left out of a workspace walk.
//
// It deliberately says nothing about dotted directories, because callers
// disagree about those and each is right: a picker should not offer .github as
// somewhere to start a session, and grep must still search it.
func NoiseDir(name string) bool { return noiseDir[strings.ToLower(name)] }
