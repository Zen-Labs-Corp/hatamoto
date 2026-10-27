package entity

import (
	"encoding/json"
	"fmt"
	"slices"
)

// PersonSchemaSlug is the slug of the built-in person type, the one built-in
// type a site may extend (add fields, change URL and archive settings).
const PersonSchemaSlug = "person"

// PersonCoreFields are the properties the person type ships with. Code reads
// them (profiles, bylines, JSON-LD, the profile form), so a site can neither
// remove nor retype them. Kept equal to the seeded schema by a test.
var PersonCoreFields = []string{
	"job_title", "short_bio", "bio", "photo", "public", "public_email",
	"website", "linkedin", "x", "github", "expertise", "on_team",
}

// coreFieldKeys are the keys of a core property that decide how it is stored
// and rendered; changing one is a retype. Titles and help text stay editable.
var coreFieldKeys = []string{"type", "format", "items", "x-hm-field", "x-media"}

// checkBuiltinUpdate decides whether an update of a built-in schema is
// allowed. Only the person type may change, and only by editing settings and
// adding properties: name, description and core properties stay as they are.
func checkBuiltinUpdate(existing *EntitySchema, in *UpdateSchemaInput) error {
	if existing.Slug != PersonSchemaSlug {
		return ErrCannotModifyBuiltin
	}
	if in.Name != nil && *in.Name != existing.Name {
		return fmt.Errorf("%w: the name of a built-in type cannot change", ErrCannotModifyBuiltin)
	}
	if in.Description != nil {
		old := ""
		if existing.Description != nil {
			old = *existing.Description
		}
		if *in.Description != old {
			return fmt.Errorf("%w: the description of a built-in type cannot change", ErrCannotModifyBuiltin)
		}
	}
	if in.Schema == nil {
		return nil
	}
	return checkPersonProperties(existing.Schema, *in.Schema)
}

// checkPersonProperties refuses a new person schema that drops or retypes a core
// property of the stored one.
func checkPersonProperties(oldRaw, newRaw json.RawMessage) error {
	props := func(raw json.RawMessage) map[string]map[string]any {
		var doc struct {
			Properties map[string]map[string]any `json:"properties"`
		}
		_ = json.Unmarshal(raw, &doc)
		return doc.Properties
	}
	oldProps, newProps := props(oldRaw), props(newRaw)
	for _, name := range PersonCoreFields {
		before, had := oldProps[name]
		if !had {
			continue // a schema that lost it before is not made worse
		}
		after, ok := newProps[name]
		if !ok {
			return fmt.Errorf("%w: %q is a core person field and cannot be removed", ErrCannotModifyBuiltin, name)
		}
		for _, k := range coreFieldKeys {
			b, _ := json.Marshal(before[k])
			a, _ := json.Marshal(after[k])
			if !slices.Equal(a, b) {
				return fmt.Errorf("%w: %q is a core person field and cannot change its type", ErrCannotModifyBuiltin, name)
			}
		}
	}
	return nil
}
