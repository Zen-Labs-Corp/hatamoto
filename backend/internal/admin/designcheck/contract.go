package designcheck

import (
	"fmt"
	"io/fs"
	"sort"

	"hatamoto.dev/core/internal/jsonx"
)

// componentSpec is one entry of components.json, the single source of
// component values (see docs/ADMIN_UI_RULES.md, Components).
type componentSpec struct {
	Selector     string                       `json:"selector"`
	Owner        string                       `json:"owner"`
	Partial      string                       `json:"partial,omitempty"`
	Variants     map[string]string            `json:"variants,omitempty"`
	Props        map[string]string            `json:"props"`
	VariantProps map[string]map[string]string `json:"variantProps,omitempty"`
}

func loadComponents(fsys fs.FS) (map[string]componentSpec, error) {
	b, err := fs.ReadFile(fsys, "components.json")
	if err != nil {
		return nil, err
	}
	var spec map[string]componentSpec
	if err := jsonx.Unmarshal(b, &spec); err != nil {
		return nil, fmt.Errorf("components.json: %w", err)
	}
	for _, name := range sortedNames(spec) {
		c := spec[name]
		for _, v := range sortedKeys(c.VariantProps) {
			if _, ok := c.Variants[v]; !ok {
				return nil, fmt.Errorf("components.json: %s has variantProps %q without a matching variants key", name, v)
			}
		}
	}
	return spec, nil
}

func sortedNames(spec map[string]componentSpec) []string {
	names := make([]string, 0, len(spec))
	for n := range spec {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
