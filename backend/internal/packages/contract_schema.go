package packages

import (
	"fmt"
	"slices"
	"strings"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/schemafield"
	"hatamoto.dev/sdk"
)

// checkEntitySchemaShipped refuses a plugin whose manifest entity_schema is not
// one of the content types its package ships (schemas/<slug>.json without a
// sections/<slug>.html). Load cannot see the package, so only install checks it.
func checkEntitySchemaShipped(p *Package, man sdk.Manifest) error {
	if man.EntitySchema == "" {
		return nil
	}
	types, err := p.EntitySchemas()
	if err != nil {
		return err
	}
	for _, ct := range types {
		if ct.Slug == man.EntitySchema {
			return nil
		}
	}
	return &ParseError{Msg: fmt.Sprintf("plugin %q declares entity_schema %q but its package has no schemas/%s.json content type", p.Name, man.EntitySchema, man.EntitySchema)}
}

// checkStandingLinkFields refuses a plugin whose standing_approval.create.link_fields
// key is not a plain {"type":"string","format":"uri"} field (schemafield.LinkFieldDef) of
// every create schema the package ships. A schema it does not ship is checked at publish
// time against the live schema.
func checkStandingLinkFields(p *Package, man sdk.Manifest) error {
	for _, r := range man.Runs {
		sa := r.StandingApproval
		if sa == nil || sa.Create == nil || len(sa.Create.LinkFields) == 0 {
			continue
		}
		types, err := p.EntitySchemas()
		if err != nil {
			return err
		}
		for _, ct := range types {
			if !slices.Contains(sa.Create.Schemas, ct.Slug) {
				continue
			}
			props := schemaProperties(ct.Schema)
			for _, k := range sa.Create.LinkFields {
				if !schemafield.LinkFieldDef(k, props[k]) {
					return &ParseError{Msg: fmt.Sprintf("plugin %q: run %q: standing_approval.create.link_fields %q is not a plain uri field of schema %q", p.Name, r.Name, k, ct.Slug)}
				}
			}
		}
	}
	return nil
}

// schemaProperties is the top-level "properties" member of a schema document,
// read as the automatic-create gate reads it; nil when absent, unreadable or
// spelled with another case.
func schemaProperties(doc []byte) map[string]any {
	var m map[string]any
	if jsonx.Unmarshal(doc, &m, jsonx.StoredLoose) != nil {
		return nil
	}
	for k := range m {
		if k != "properties" && strings.EqualFold(k, "properties") {
			return nil
		}
	}
	props, _ := m["properties"].(map[string]any)
	return props
}
