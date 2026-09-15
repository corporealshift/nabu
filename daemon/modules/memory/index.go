package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The index rides in front of the model until the next compaction, so its cost
// is standing rather than one-off. Spec 11.2 sets the caps.
const (
	// maxIndexLines is one line per memory, capped.
	maxIndexLines = 200
	// maxIndexBytes caps the rendered index.
	maxIndexBytes = 25 << 10
)

// BuildIndex renders the index and reports whether it is over its caps. Over
// the cap it is still written whole: until consolidation lands, refusing a save
// would lose a fact, which costs more than an over-long index does.
func BuildIndex(mems []Memory) (content string, notice string) {
	content = FormatIndex(mems)
	switch {
	case len(mems) > maxIndexLines:
		notice = fmt.Sprintf(
			"The memory index holds %d memories, over its cap of %d. "+
				"It needs consolidating: merge memories that say the same thing "+
				"and forget the ones that no longer hold.",
			len(mems), maxIndexLines)
	case len(content) > maxIndexBytes:
		notice = fmt.Sprintf(
			"The memory index is %d bytes, over its cap of %d. "+
				"It needs consolidating: shorten the descriptions that carry the least.",
			len(content), maxIndexBytes)
	}
	return content, notice
}

// FormatIndex renders one line per memory, ordered by name so the same
// memories always produce the same bytes.
func FormatIndex(mems []Memory) string {
	if len(mems) == 0 {
		return ""
	}
	sorted := append([]Memory(nil), mems...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	var b strings.Builder
	for _, m := range sorted {
		b.WriteString(indexLine(m))
	}
	return b.String()
}

// indexLine is `- [Title](file.md) — hook`, the shape the existing index uses.
// The hook is what makes a reader open the file, so it stays separate.
func indexLine(m Memory) string {
	title, hook := splitDescription(m.Description)
	if title == "" {
		title = m.Name
	}
	line := "- [" + title + "](" + m.Name + ".md)"
	if hook != "" {
		line += " — " + hook
	}
	if m.Imported {
		// The model needs to know before it tries to edit one in place.
		line += " *(imported)*"
	}
	return line + "\n"
}

// splitDescription cuts at the first semicolon or dash, which is where a
// description written for this format already separates subject from hook.
func splitDescription(desc string) (title, hook string) {
	desc = strings.TrimSpace(desc)
	for _, sep := range []string{";", " — ", " - "} {
		if before, after, ok := strings.Cut(desc, sep); ok {
			return strings.TrimSpace(before), strings.TrimSpace(after)
		}
	}
	return desc, ""
}

// WriteIndex rebuilds a store's MEMORY.md from what is on disk and returns any
// cap notice. An empty store has no index file at all.
func WriteIndex(s *Store) (string, error) {
	if s.imported {
		return "", nil // another tool owns that directory's index
	}
	mems, err := s.Load()
	if err != nil {
		return "", err
	}
	path := filepath.Join(s.dir, IndexFile)
	content, notice := BuildIndex(mems)
	if content == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return "", err
		}
		return "", nil
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return "", err
	}
	return notice, os.WriteFile(path, []byte(content), 0o644)
}
