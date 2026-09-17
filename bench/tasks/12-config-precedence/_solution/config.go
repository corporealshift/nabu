package fixture

// Layer is one source of settings. A key that is absent from the map was not
// mentioned by that layer at all.
type Layer map[string]string

// Resolve merges the layers into final settings. Later layers win.
//
// Presence is what counts, not truth: a layer that sets a key to the empty
// string has made a decision, and a layer that omits the key has not.
func Resolve(defaults, file, env, flags Layer) map[string]string {
	out := map[string]string{}
	for _, layer := range []Layer{defaults, file, env, flags} {
		for k, v := range layer {
			out[k] = v
		}
	}
	return out
}
