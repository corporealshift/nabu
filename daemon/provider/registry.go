package provider

import (
	"fmt"
	"sort"
	"strings"
)

// Registry maps configured provider names to Providers and resolves a
// session's model string: "name/model" selects a provider by name; a bare
// model, or an unknown prefix, goes to the default provider unchanged.
type Registry struct {
	def       string
	providers map[string]Provider
	cfgs      map[string]Config
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{providers: map[string]Provider{}, cfgs: map[string]Config{}}
}

// Add registers a provider. The first added, or any added with isDefault,
// becomes the default.
func (r *Registry) Add(cfg Config, p Provider, isDefault bool) {
	r.providers[cfg.Name] = p
	r.cfgs[cfg.Name] = cfg.withDefaults()
	if isDefault || r.def == "" {
		r.def = cfg.Name
	}
}

// Resolve returns the provider, the model name to send it, and its config.
func (r *Registry) Resolve(model string) (Provider, string, Config, error) {
	if name, rest, ok := strings.Cut(model, "/"); ok {
		if p, found := r.providers[name]; found {
			return p, rest, r.cfgs[name], nil
		}
	}
	if r.def == "" {
		return nil, "", Config{}, fmt.Errorf("no providers configured")
	}
	return r.providers[r.def], model, r.cfgs[r.def], nil
}

// Names lists configured provider names, sorted.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.providers))
	for n := range r.providers {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
