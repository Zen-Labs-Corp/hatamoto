package entity

import (
	"context"
	"encoding/json/jsontext"
	"fmt"
	"sort"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/shortcode"
)

// partSlotLabels names a theme part's slot for people.
var partSlotLabels = map[string]string{
	"header": "Header", "footer": "Footer",
	"single": "Single template", "archive": "Archive template",
}

// ScanFormCandidates calls fn, newest first, for each entity whose attributes
// text may hold a form widget, section or shortcode, until fn returns false.
// It is a sequence scan: used only by the form usage index.
func (r *PostgresEntityRepository) ScanFormCandidates(ctx context.Context, fn func(*Entity) bool) error {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	rows, err := conn.Query(ctx, `
		SELECT e.id, e.name, e.slug, e.status, e.attributes, e.updated_at, s.slug
		FROM hatamoto_entities e
		JOIN hatamoto_entity_schemas s ON s.id = e.schema_id
		WHERE (e.attributes::text LIKE '%"form"%' OR e.attributes::text LIKE '%[form%'
		       OR e.attributes::text LIKE '%contact\_form%')
		ORDER BY e.updated_at DESC, e.id`)
	if err != nil {
		return fmt.Errorf("scan entities for forms: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var e Entity
		var schemaSlug string
		if err := rows.Scan(&e.ID, &e.Name, &e.Slug, &e.Status, &e.Attributes, &e.UpdatedAt, &schemaSlug); err != nil {
			return err
		}
		e.Schema = &EntitySchema{Slug: schemaSlug}
		if !fn(&e) {
			return nil
		}
	}
	return rows.Err()
}

// entityFormPlacements decodes e and returns its form placements by slug, one
// per slug and way. The doc is decoded, never matched as text; page_sections
// is read only when there is no page_doc (it is the frozen mirror otherwise).
func entityFormPlacements(e *Entity) map[string][]FormPlacement {
	var m map[string]jsontext.Value
	if jsonx.Unmarshal(e.Attributes, &m, jsonx.StoredLoose) != nil {
		return nil
	}
	schemaSlug := ""
	if e.Schema != nil {
		schemaSlug = e.Schema.Slug
	}
	base := FormPlacement{EntityID: e.ID, SchemaSlug: schemaSlug, Status: string(e.Status), Name: e.Name, Slug: e.Slug, Kind: PlacementPage}
	switch schemaSlug {
	case PartSchemaSlug:
		var slot string
		if raw, ok := m["slot"]; ok {
			_ = jsonx.Unmarshal(raw, &slot, jsonx.StoredLoose)
		}
		base.Area = partSlotLabels[slot]
		if base.Area == "" {
			base.Area = e.Name
		}
		base.Kind, base.Name, base.Slug = PlacementArea, base.Area, ""
	case BlockSchemaSlug:
		base.Kind, base.Area, base.Slug = PlacementArea, e.Name, ""
	}

	found := map[string]map[string]bool{} // slug -> via set
	add := func(slug, via string) {
		if slug == "" {
			return
		}
		if found[slug] == nil {
			found[slug] = map[string]bool{}
		}
		found[slug][via] = true
	}

	rawDoc := m["page_doc"]
	hasDoc := len(rawDoc) > 0 && string(rawDoc) != "null"
	if hasDoc {
		if d, err := builder.Parse(rawDoc); err == nil {
			for _, n := range builder.NodesOfKind(d, "form") {
				s, _ := n.P["slug"].(string)
				add(s, ViaWidget)
			}
			for _, n := range builder.NodesOfKind(d, "component") {
				if t, _ := n.P["type"].(string); t == "contact_form" {
					add(contentFormSlug(n.P["content"]), ViaSection)
				}
			}
		}
	}
	for key, raw := range m {
		if key == "page_sections" && hasDoc {
			continue
		}
		var v any
		if jsonx.Unmarshal(raw, &v, jsonx.StoredLoose) != nil {
			continue
		}
		if key == "page_sections" {
			if secs, ok := v.([]any); ok {
				for _, sec := range secs {
					sm, _ := sec.(map[string]any)
					if t, _ := sm["type"].(string); t == "contact_form" {
						add(contentFormSlug(sm["content"]), ViaSection)
					}
				}
			}
		}
		walkStrings(v, func(s string) {
			for _, slug := range shortcode.Slugs(s, "form") {
				add(slug, ViaShortcode)
			}
		})
	}

	out := map[string][]FormPlacement{}
	for slug, vias := range found {
		names := make([]string, 0, len(vias))
		for via := range vias {
			names = append(names, via)
		}
		sort.Strings(names)
		for _, via := range names {
			p := base
			p.Via = via
			out[slug] = append(out[slug], p)
		}
	}
	return out
}

func contentFormSlug(content any) string {
	c, _ := content.(map[string]any)
	s, _ := c["form_slug"].(string)
	return s
}

// walkStrings calls fn for every string in a decoded JSON value.
func walkStrings(v any, fn func(string)) {
	switch t := v.(type) {
	case string:
		fn(t)
	case []any:
		for _, x := range t {
			walkStrings(x, fn)
		}
	case map[string]any:
		for _, x := range t {
			walkStrings(x, fn)
		}
	}
}
