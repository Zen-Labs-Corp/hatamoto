package entity

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// ListFilter narrows a public listing. Only published base rows (never
// variants) are listed; every set field must match.
type ListFilter struct {
	// SchemaSlugs restricts to these schemas (empty: any schema).
	SchemaSlugs []string
	// TermID restricts to entities attached to the term.
	TermID uuid.UUID
	// AuthorID restricts to entities written by this author.
	AuthorID uuid.UUID
	// PersonID restricts to entities credited to this person.
	PersonID uuid.UUID
	// Search is a websearch-syntax query matched against the entity's
	// full-text vector (name, excerpt, content), best match first. Entities
	// marked no_index never appear in search results.
	Search string
	// Attr restricts to entities whose attribute Attr equals AttrEq (a string,
	// or the number/boolean AttrEq spells), or that have the attribute at all
	// when AttrEq is empty and AttrEqSet is false. With AttrEqSet an empty
	// AttrEq matches the empty string only. The match is a JSONB containment test (@>), served
	// by the GIN index on attributes. An invalid key matches nothing.
	Attr, AttrEq string
	AttrEqSet    bool
	// IncludeScheduled adds scheduled entities (published_at is their date) to
	// the published ones. Drafts and archived entities never appear.
	IncludeScheduled bool
	// Order is "" (newest first), "-published_at", "published_at", "name",
	// "-name", "updated_at", "-updated_at", "attr:<key>" or "-attr:<key>" (numeric values sort as numbers,
	// before the rest; entities lacking the attribute last). Anything else
	// gives the default order.
	Order string
	// ExcludeID leaves this entity out (e.id <> ExcludeID) when set.
	ExcludeID uuid.UUID
	// Offset skips this many more rows on top of (page-1)*perPage; clamped to
	// 0..MaxListOffset. The returned total stays the raw match count.
	Offset int
}

// MaxListOffset caps ListFilter.Offset.
const MaxListOffset = 1000

// attrKeyRE is a valid attribute key for Attr and "attr:<key>" orders.
var attrKeyRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ValidAttrKey reports whether k can be used as a filter or order attribute.
func ValidAttrKey(k string) bool { return attrKeyRE.MatchString(k) }

// attrNumberRE matches the text of a plain decimal number.
const attrNumberRE = `^-?[0-9]+(\.[0-9]+)?$`

// orderSQL is the ORDER BY of a listing for spec; arg binds a parameter.
func orderSQL(spec string, arg func(any) string) string {
	const newest = "e.published_at DESC NULLS LAST, e.created_at DESC, e.id"
	dir := "ASC"
	if strings.HasPrefix(spec, "-") {
		dir, spec = "DESC", spec[1:]
	}
	switch {
	case spec == "published_at":
		return "e.published_at " + dir + " NULLS LAST, e.created_at " + dir + ", e.id"
	case spec == "updated_at":
		return "e.updated_at " + dir + ", e.id"
	case spec == "name":
		return "e.name " + dir + ", e.id"
	case strings.HasPrefix(spec, "attr:") && ValidAttrKey(spec[5:]):
		k := arg(spec[5:])
		return "CASE WHEN e.attributes->>" + k + "::text ~ '" + attrNumberRE + "' THEN (e.attributes->>" + k + "::text)::numeric END " +
			dir + " NULLS LAST, e.attributes->>" + k + "::text " + dir + " NULLS LAST, " + newest
	}
	return newest
}

// attrContains are the JSONB documents an attribute equal to v matches.
func attrContains(key, v string) []string {
	forms := []any{v}
	// NaN and Inf parse but are not JSON numbers: they stay plain strings.
	if f, err := strconv.ParseFloat(v, 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) && strconv.FormatFloat(f, 'f', -1, 64) == v {
		forms = append(forms, json.Number(v))
	}
	if v == "true" || v == "false" {
		forms = append(forms, v == "true")
	}
	out := make([]string, 0, len(forms))
	for _, f := range forms {
		if b, err := json.Marshal(map[string]any{key: f}); err == nil {
			out = append(out, string(b))
		}
	}
	return out
}

// ListPublishedFiltered returns one page of published entities matching f and
// the total number of matches. Without a search the order is newest first.
func (r *PostgresPublicRepository) ListPublishedFiltered(ctx context.Context, f ListFilter, page, perPage int) ([]*Entity, int, error) {
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	page = max(page, 1)
	if perPage < 1 || perPage > 100 {
		perPage = 12
	}

	status := "e.status = 'published'"
	if f.IncludeScheduled {
		status = "e.status IN ('published', 'scheduled')"
	}
	var (
		where []string
		args  []any
	)
	rank := ""
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	where = []string{status, "e.variant_key IS NULL", HiddenPersonFor(arg(VisibleLocales(ctx)))}
	if f.Attr != "" {
		switch {
		case !ValidAttrKey(f.Attr):
			where = append(where, "FALSE")
		case f.AttrEq == "" && !f.AttrEqSet:
			where = append(where, "e.attributes ? "+arg(f.Attr)+"::text")
		default:
			var alts []string
			for _, doc := range attrContains(f.Attr, f.AttrEq) {
				alts = append(alts, "e.attributes @> "+arg(doc)+"::jsonb")
			}
			if len(alts) == 0 {
				alts = []string{"FALSE"}
			}
			where = append(where, "("+strings.Join(alts, " OR ")+")")
		}
	}
	if len(f.SchemaSlugs) > 0 {
		where = append(where, "s.slug = ANY("+arg(f.SchemaSlugs)+")")
	}
	if f.TermID != uuid.Nil {
		where = append(where, "EXISTS (SELECT 1 FROM hatamoto_entity_terms et WHERE et.entity_id = e.id AND et.term_id = "+arg(f.TermID)+")")
	}
	if f.AuthorID != uuid.Nil {
		where = append(where, "e.author_id = "+arg(f.AuthorID))
	}
	if f.PersonID != uuid.Nil {
		where = append(where, "e.byline_person_id = "+arg(f.PersonID))
	}
	if f.ExcludeID != uuid.Nil {
		where = append(where, "e.id <> "+arg(f.ExcludeID))
	}
	if q := strings.TrimSpace(f.Search); q != "" {
		p := arg(q)
		where = append(where,
			"e.search_vector @@ websearch_to_tsquery('simple', "+p+")",
			"NOT "+NoIndexSQL)
		rank = "ts_rank(e.search_vector, websearch_to_tsquery('simple', " + p + ")) DESC, "
	}
	cond := strings.Join(where, " AND ")
	from := `FROM hatamoto_entities e JOIN hatamoto_entity_schemas s ON e.schema_id = s.id WHERE ` + cond

	var total int
	if err := conn.QueryRow(ctx, "SELECT COUNT(*) "+from, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count entities: %w", err)
	}
	order := rank + orderSQL(f.Order, arg) // after the count: its parameters are not part of it
	limit, offset := arg(perPage), arg((page-1)*perPage+min(max(f.Offset, 0), MaxListOffset))
	rows, err := conn.Query(ctx, `
		SELECT e.id, e.schema_id, e.name, e.slug, e.locale, e.status, e.attributes, e.metadata,
			e.version, e.created_by, e.updated_by, e.created_at, e.updated_at, e.published_at,
			COALESCE(e.editorial, '{}'::jsonb),
			e.parent_id,
			s.id, s.name, s.slug, s.description, s.schema, s.is_builtin, s.created_at, s.updated_at, s.settings
		`+from+` ORDER BY `+order+` LIMIT `+limit+` OFFSET `+offset, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list entities: %w", err)
	}
	defer rows.Close()

	items := []*Entity{}
	for rows.Next() {
		var e Entity
		var s EntitySchema
		if err := rows.Scan(
			&e.ID, &e.SchemaID, &e.Name, &e.Slug, &e.Locale, &e.Status, &e.Attributes, &e.Metadata,
			&e.Version, &e.CreatedBy, &e.UpdatedBy, &e.CreatedAt, &e.UpdatedAt, &e.PublishedAt,
			&e.Editorial,
			&e.ParentID,
			&s.ID, &s.Name, &s.Slug, &s.Description, &s.Schema, &s.IsBuiltin, &s.CreatedAt, &s.UpdatedAt, &s.Settings,
		); err != nil {
			return nil, 0, fmt.Errorf("failed to scan entity: %w", err)
		}
		e.Schema = &s
		items = append(items, &e)
	}
	return items, total, rows.Err()
}

// NoIndexSQL is true for an entity row aliased e that must stay out of
// sitemaps and search: metadata.no_index, attributes.no_index or an
// attributes.seo.robots value containing "noindex".
const NoIndexSQL = `(COALESCE(e.metadata->>'no_index', 'false') = 'true'
	OR COALESCE(e.attributes->>'no_index', 'false') = 'true'
	OR COALESCE(e.attributes->'seo'->>'robots', '') ILIKE '%noindex%')`

// HiddenPersonFor returns the public visibility predicate (schema aliased s,
// entity e); ph is the placeholder of a text[] argument holding the visible
// locales (bind VisibleLocales(ctx); a NULL argument means the site language).
// It is false for a person entity whose attributes.public is not true, and
// for any entity of the built-in block or theme_part type whatever its status
// (they are used by reference, never served). Every public read adds it, so
// these are absent from pages, sitemaps, llms.txt, search, theme queries and
// MCP public search alike.
//
// It also hides every entity whose locale is not visible: without a locale
// scope that is every locale but the site language, so other locales are
// stored but not public.
func HiddenPersonFor(ph string) string {
	return `((s.slug <> 'person' OR COALESCE(e.attributes->>'public', 'false') = 'true')
	AND NOT (s.is_builtin AND s.slug IN ('block', 'theme_part'))
	AND ` + visibleLocaleSQL(ph) + `)`
}

// visibleLocaleSQL is the locale term of HiddenPersonFor for queries that do
// not join the schema.
func visibleLocaleSQL(ph string) string {
	return `e.locale = ANY(COALESCE(` + ph + `::text[], ARRAY[(SELECT public.hatamoto_site_language())]))`
}

// AttrValueCount is one distinct attribute value and how many published
// entities carry it.
type AttrValueCount struct {
	Value string
	Count int
}

// ListAttrValues returns the distinct non-empty string values of the
// attribute attrKey across the published base entities of the given schemas,
// with counts, sorted by value, at most limit (1..500). Entities where the
// attribute is not a string are ignored. One aggregate query; the key
// presence test uses the GIN index on attributes.
func (r *PostgresPublicRepository) ListAttrValues(ctx context.Context, schemaSlugs []string, attrKey string, limit int) ([]AttrValueCount, error) {
	if !ValidAttrKey(attrKey) || len(schemaSlugs) == 0 {
		return nil, nil
	}
	limit = min(max(limit, 1), 500)
	conn, err := r.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `
		SELECT e.attributes->>$2::text AS v, COUNT(*)
		FROM hatamoto_entities e JOIN hatamoto_entity_schemas s ON e.schema_id = s.id
		WHERE s.slug = ANY($1) AND e.status = 'published' AND e.variant_key IS NULL
		  AND `+HiddenPersonFor("$4")+`
		  AND e.attributes ? $2::text
		  AND jsonb_typeof(e.attributes->$2::text) = 'string'
		  AND e.attributes->>$2::text <> ''
		GROUP BY v ORDER BY v LIMIT $3`, schemaSlugs, attrKey, limit, VisibleLocales(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to list attribute values: %w", err)
	}
	defer rows.Close()
	var out []AttrValueCount
	for rows.Next() {
		var c AttrValueCount
		if err := rows.Scan(&c.Value, &c.Count); err != nil {
			return nil, fmt.Errorf("failed to scan attribute value: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
