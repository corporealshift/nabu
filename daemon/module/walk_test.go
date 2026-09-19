package module

import "testing"

func TestNoiseDir(t *testing.T) {
	for _, name := range []string{".git", "node_modules", "build", "target", "vendor", "dist", "__pycache__"} {
		if !NoiseDir(name) {
			t.Errorf("NoiseDir(%q) = false, want true", name)
		}
	}
	// Case, because Windows does not care and neither should this.
	if !NoiseDir("Build") || !NoiseDir("NODE_MODULES") {
		t.Error("NoiseDir should not care about case")
	}
	for _, name := range []string{"daemon", "src", "cmd", "protocol", "buildings", "distribution"} {
		if NoiseDir(name) {
			t.Errorf("NoiseDir(%q) = true, want false", name)
		}
	}
}

// Callers disagree about dotted directories and each is right, so the shared
// helper must not decide for them: a picker should not offer .github as a place
// to start a session, and grep must still search it.
func TestNoiseDirSaysNothingAboutDottedDirectories(t *testing.T) {
	for _, name := range []string{".github", ".vscode", ".kotlin", ".gradle", ".venv"} {
		if NoiseDir(name) {
			t.Errorf("NoiseDir(%q) = true; the dotted decision belongs to the caller", name)
		}
	}
	// Except .git, which nothing should ever walk into.
	if !NoiseDir(".git") {
		t.Error(".git is noise everywhere, dotted or not")
	}
}
