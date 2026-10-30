package localeconv

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
)

// SchemaChange is a schema whose settings the conversion rewrites.
type SchemaChange struct {
	ID     string
	Slug   string
	Before json.RawMessage
	After  json.RawMessage
}

// overrideMatchKeys are the override keys that say what the override matches.
var overrideMatchKeys = []string{"attribute", "equals"}

// StripSitemapGroup removes the sitemap_group key from every override of every
// schema that matches on attribute. The override itself stays: it carries the
// url_pattern, and dropping it would change URLs. It returns the schemas with
// their new settings (only changed ones differ), the overrides it edited and
// the keys those overrides keep besides attribute and equals.
func StripSitemapGroup(schemas []Schema, attribute string) (out []Schema, changed []OverrideChange, keptKeys []string, err error) {
	out = slices.Clone(schemas)
	kept := map[string]bool{}
	for i, s := range out {
		var settings map[string]json.RawMessage
		if len(bytes.TrimSpace(s.Settings)) == 0 || json.Unmarshal(s.Settings, &settings) != nil {
			continue // settings the URL registry cannot read have no overrides to edit
		}
		rawOverrides, ok := settings["overrides"]
		if !ok {
			continue
		}
		var overrides []map[string]json.RawMessage
		if err := json.Unmarshal(rawOverrides, &overrides); err != nil {
			continue
		}
		edited := false
		for _, o := range overrides {
			var attr string
			if json.Unmarshal(o["attribute"], &attr) != nil || attr != attribute {
				continue
			}
			group, has := o["sitemap_group"]
			if !has {
				continue
			}
			var g, equals string
			_ = json.Unmarshal(group, &g)
			_ = json.Unmarshal(o["equals"], &equals)
			changed = append(changed, OverrideChange{Schema: s.Slug, Attribute: attr, Equals: equals, Group: g})
			delete(o, "sitemap_group")
			edited = true
			for k := range o {
				if !slices.Contains(overrideMatchKeys, k) {
					kept[k] = true
				}
			}
		}
		if !edited {
			continue
		}
		if settings["overrides"], err = json.Marshal(overrides); err != nil {
			return nil, nil, nil, fmt.Errorf("encode overrides of %s: %w", s.Slug, err)
		}
		if out[i].Settings, err = json.Marshal(settings); err != nil {
			return nil, nil, nil, fmt.Errorf("encode settings of %s: %w", s.Slug, err)
		}
	}
	slices.SortFunc(changed, func(a, b OverrideChange) int {
		return cmp.Or(cmp.Compare(a.Schema, b.Schema), cmp.Compare(a.Equals, b.Equals))
	})
	keptKeys = make([]string, 0, len(kept))
	for k := range kept {
		keptKeys = append(keptKeys, k)
	}
	slices.Sort(keptKeys)
	return out, changed, keptKeys, nil
}

// stillMatchesAttribute reports whether any schema override matches on attribute.
func stillMatchesAttribute(schemas []Schema, attribute string) bool {
	for _, s := range schemas {
		var settings struct {
			Overrides []struct {
				Attribute string `json:"attribute"`
			} `json:"overrides"`
		}
		if json.Unmarshal(s.Settings, &settings) != nil {
			continue
		}
		if slices.ContainsFunc(settings.Overrides, func(o struct {
			Attribute string `json:"attribute"`
		}) bool {
			return o.Attribute == attribute
		}) {
			return true
		}
	}
	return false
}
