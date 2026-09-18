package fixture

// Dedupe removes repeated neighbours, keeping the first of each run:
// "a a b a" becomes "a b a". An element that comes back later is kept,
// because only a repeat that is adjacent is a repeat.
func Dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
