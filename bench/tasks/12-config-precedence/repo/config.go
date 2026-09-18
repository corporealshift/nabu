package fixture

// Layer is one source of settings. A key that is absent from the map was not
// mentioned by that layer at all.
type Layer map[string]string

// Resolve merges the layers into final settings. Later layers win.
func Resolve(defaults, file, env, flags Layer) map[string]string {
	panic("not implemented")
}
