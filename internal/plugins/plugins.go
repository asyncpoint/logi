// Package plugins lists the built-in (compiled-in) plugins and builds the
// parser registry from configuration. There is no runtime
// plugin loading.
package plugins

import (
	"fmt"

	"github.com/asyncpoint/logi/internal/parser"
	"github.com/asyncpoint/logi/internal/plugins/ncache"
)

// All returns every compiled-in plugin.
func All() []parser.Plugin { return []parser.Plugin{ncache.New()} }

// BuildRegistry registers the core profiles plus every plugin that is enabled.
// enabled overrides a plugin's default; unknown names are an error.
func BuildRegistry(enabled map[string]bool) (*parser.Registry, error) {
	r := parser.NewRegistry()
	if err := parser.RegisterCore(r); err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, p := range All() {
		known[p.Name()] = true
		on := p.EnabledByDefault()
		if v, ok := enabled[p.Name()]; ok {
			on = v
		}
		if on {
			if err := p.Register(r); err != nil {
				return nil, err
			}
		}
	}
	for name := range enabled {
		if !known[name] {
			return nil, fmt.Errorf("unknown plugin %q", name)
		}
	}
	return r, nil
}
