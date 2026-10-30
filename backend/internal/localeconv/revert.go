package localeconv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrConversionNotFound is returned by Revert for an unknown conversion id.
var ErrConversionNotFound = errors.New("conversion not found")

// maxStrandedSample caps the stranded rows listed individually.
const maxStrandedSample = 20

type conversionRecord struct {
	appliedAt                     time.Time
	status                        string
	plan                          Plan
	settingsBefore, settingsAfter settingsJSON
	schemasBefore, schemasAfter   []schemaRecord
	rows                          int
}

func loadConversion(ctx context.Context, q querier, id int64) (*conversionRecord, error) {
	var c conversionRecord
	var plan, sBefore, sAfter, schBefore, schAfter []byte
	err := q.QueryRow(ctx, `
		SELECT applied_at, status, plan, settings_before, settings_after, schemas_before, schemas_after,
		       (SELECT count(*) FROM hatamoto_locale_conversion_rows WHERE conversion_id = c.id)
		  FROM hatamoto_locale_conversions c WHERE id = $1`, id).
		Scan(&c.appliedAt, &c.status, &plan, &sBefore, &sAfter, &schBefore, &schAfter, &c.rows)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("conversion %d: %w", id, ErrConversionNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("load conversion %d: %w", id, err)
	}
	for _, d := range []struct {
		raw []byte
		dst any
	}{{plan, &c.plan}, {sBefore, &c.settingsBefore}, {sAfter, &c.settingsAfter}, {schBefore, &c.schemasBefore}, {schAfter, &c.schemasAfter}} {
		if err := json.Unmarshal(d.raw, d.dst); err != nil {
			return nil, fmt.Errorf("decode conversion %d: %w", id, err)
		}
	}
	return &c, nil
}

// Revert undoes a conversion in one locked transaction, in the reverse order of
// Apply: parents, home slugs, locale and group, renamed slugs, stripped
// attributes, schemas, and the settings last (entities first, so the retag
// trigger finds nothing to move). It refuses, listing everything, when data
// changed since the apply; force overrides that, except for already_reverted
// and newer_conversion.
func (s *pgService) Revert(ctx context.Context, id int64, force bool, by string) (*RevertResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockWrites(ctx, tx); err != nil {
		return nil, err
	}

	c, err := loadConversion(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if c.status == "reverted" {
		return nil, &BlockingError{Issues: []Issue{{Code: CodeAlreadyReverted, Message: fmt.Sprintf("conversion %d is already reverted", id)}}}
	}
	var newer int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(id), 0) FROM hatamoto_locale_conversions WHERE id > $1 AND status = 'applied'`, id).Scan(&newer); err != nil {
		return nil, fmt.Errorf("check newer conversions: %w", err)
	}
	if newer != 0 {
		return nil, &BlockingError{Issues: []Issue{{Code: CodeNewerConversion, Message: fmt.Sprintf("conversion %d was applied after this one, revert it first", newer)}}}
	}

	conflicts, err := findConflicts(ctx, tx, id, c)
	if err != nil {
		return nil, err
	}
	stranded, strandedCount, err := findStranded(ctx, tx, id, c)
	if err != nil {
		return nil, err
	}
	res := &RevertResult{ConversionID: id, Forced: []Issue{}, StrandedItems: stranded, Stranded: strandedCount}
	if len(conflicts) > 0 || strandedCount > 0 {
		refusal := slicesConcat(conflicts, stranded)
		if strandedCount > len(stranded) {
			refusal = append(refusal, Issue{Code: CodeRevertConflict, Message: fmt.Sprintf("%d more rows created since the conversion are not listed", strandedCount-len(stranded))})
		}
		if !force {
			return nil, &BlockingError{Issues: refusal}
		}
		res.Forced = conflicts
	}

	if _, err := tx.Exec(ctx, `SELECT set_config('hatamoto.import', 'on', true)`); err != nil {
		return nil, fmt.Errorf("set import mode: %w", err)
	}
	home := c.plan.Home.Slug
	const from = ` FROM hatamoto_locale_conversion_rows r WHERE r.conversion_id = $1 AND e.id = r.entity_id`
	steps := []struct {
		name string
		sql  string
		args []any
	}{
		{"parents", `UPDATE hatamoto_entities e SET parent_id = r.before_parent_id` + from + ` AND e.parent_id IS DISTINCT FROM r.before_parent_id`, []any{id}},
		{"home slugs", `UPDATE hatamoto_entities e SET slug = r.before_slug` + from + ` AND r.before_slug <> r.after_slug AND r.after_slug = $2`, []any{id, home}},
		{"locale and group", `UPDATE hatamoto_entities e SET locale = r.before_locale, translation_group = r.before_group` + from, []any{id}},
		{"renamed slugs", `UPDATE hatamoto_entities e SET slug = r.before_slug` + from + ` AND r.before_slug <> r.after_slug AND r.after_slug <> $2 AND e.slug <> r.before_slug`, []any{id, home}},
		{"attributes", `UPDATE hatamoto_entities e SET attributes = e.attributes || r.before_attrs` + from + ` AND r.before_attrs IS NOT NULL`, []any{id}},
	}
	for _, st := range steps {
		tag, err := tx.Exec(ctx, st.sql, st.args...)
		if err != nil {
			return nil, fmt.Errorf("revert (%s): %w", st.name, err)
		}
		if st.name == "locale and group" {
			res.Restored = int(tag.RowsAffected())
		}
	}
	for _, sc := range c.schemasBefore {
		if _, err := tx.Exec(ctx, `UPDATE hatamoto_entity_schemas SET settings = $2::jsonb WHERE id = $1::uuid`, sc.ID, []byte(sc.Settings)); err != nil {
			return nil, fmt.Errorf("revert schema %s: %w", sc.ID, err)
		}
	}
	b := c.settingsBefore
	if _, err := tx.Exec(ctx, `
		UPDATE hatamoto_site_config SET language = $1, locales = $2::jsonb, prefix_all = $3, x_default_locale = $4,
		       homepage_entity_id = $5::uuid
		 WHERE id = TRUE`, b.Language, []byte(b.Locales), b.PrefixAll, b.XDefaultLocale, b.HomepageEntityID); err != nil {
		return nil, fmt.Errorf("revert settings: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE hatamoto_locale_conversions SET status = 'reverted', reverted_at = now(), reverted_by = $2 WHERE id = $1`, id, by); err != nil {
		return nil, fmt.Errorf("mark reverted: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return res, nil
}

func slicesConcat(a, b []Issue) []Issue {
	out := make([]Issue, 0, len(a)+len(b))
	return append(append(out, a...), b...)
}

// findConflicts lists what changed since the apply: recorded rows that were
// deleted, moved or edited, schemas whose settings differ and locale settings
// that differ from what the conversion wrote.
func findConflicts(ctx context.Context, q querier, id int64, c *conversionRecord) ([]Issue, error) {
	var out []Issue
	rows, err := q.Query(ctx, `
		SELECT r.entity_id::text, r.after_slug, e.id IS NULL,
		       e.id IS NOT NULL AND (e.locale, e.slug, e.parent_id, e.translation_group)
		           IS DISTINCT FROM (r.after_locale, r.after_slug, r.after_parent_id, r.after_group),
		       e.id IS NOT NULL AND e.updated_at IS DISTINCT FROM r.after_updated_at
		  FROM hatamoto_locale_conversion_rows r LEFT JOIN hatamoto_entities e ON e.id = r.entity_id
		 WHERE r.conversion_id = $1 ORDER BY r.entity_id`, id)
	if err != nil {
		return nil, fmt.Errorf("compare rows: %w", err)
	}
	for rows.Next() {
		var entityID, slug string
		var gone, moved, edited bool
		if err := rows.Scan(&entityID, &slug, &gone, &moved, &edited); err != nil {
			rows.Close()
			return nil, err
		}
		switch {
		case gone:
			out = append(out, Issue{Code: CodeRevertConflict, EntityID: entityID, Message: fmt.Sprintf("%q was deleted since the conversion", slug)})
		case moved:
			out = append(out, Issue{Code: CodeRevertConflict, EntityID: entityID, Message: fmt.Sprintf("%q was moved or re-linked since the conversion", slug)})
		case edited:
			out = append(out, Issue{Code: CodeRevertConflict, EntityID: entityID, Message: fmt.Sprintf("%q was edited since the conversion", slug)})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, sc := range c.schemasAfter {
		var same bool
		if err := q.QueryRow(ctx, `SELECT COALESCE((SELECT settings = $2::jsonb FROM hatamoto_entity_schemas WHERE id = $1::uuid), false)`,
			sc.ID, []byte(sc.Settings)).Scan(&same); err != nil {
			return nil, fmt.Errorf("compare schema: %w", err)
		}
		if !same {
			out = append(out, Issue{Code: CodeRevertConflict, EntityID: sc.ID, Message: "a schema's settings changed since the conversion"})
		}
	}

	a := c.settingsAfter
	var same bool
	if err := q.QueryRow(ctx, `
		SELECT (language, locales, prefix_all, x_default_locale, homepage_entity_id::text) IS NOT DISTINCT FROM ($1::text, $2::jsonb, $3::boolean, $4::text, $5::text)
		  FROM hatamoto_site_config WHERE id = TRUE`, a.Language, []byte(a.Locales), a.PrefixAll, a.XDefaultLocale, a.HomepageEntityID).Scan(&same); err != nil {
		return nil, fmt.Errorf("compare settings: %w", err)
	}
	if !same {
		out = append(out, Issue{Code: CodeRevertConflict, Message: "the locale settings changed since the conversion"})
	}
	return out, nil
}

// findStranded lists rows created after the apply in a converted locale; revert
// leaves them alone. It returns a sample and the total.
func findStranded(ctx context.Context, q querier, id int64, c *conversionRecord) ([]Issue, int, error) {
	tags := make([]string, len(c.plan.Locales))
	for i, l := range c.plan.Locales {
		tags[i] = normalized(l.Tag)
	}
	rows, err := q.Query(ctx, `
		SELECT e.id::text, e.slug, e.locale FROM hatamoto_entities e
		 WHERE e.created_at > $2 AND e.locale = ANY($3::text[]) AND e.variant_key IS NULL
		   AND NOT EXISTS (SELECT 1 FROM hatamoto_locale_conversion_rows r WHERE r.conversion_id = $1 AND r.entity_id = e.id)
		 ORDER BY e.created_at, e.id`, id, c.appliedAt, tags)
	if err != nil {
		return nil, 0, fmt.Errorf("find rows created since: %w", err)
	}
	defer rows.Close()
	sample := []Issue{}
	total := 0
	for rows.Next() {
		var entityID, slug, locale string
		if err := rows.Scan(&entityID, &slug, &locale); err != nil {
			return nil, 0, err
		}
		total++
		if len(sample) < maxStrandedSample {
			sample = append(sample, Issue{Code: CodeRevertConflict, EntityID: entityID,
				Message: fmt.Sprintf("%q was created in locale %s after the conversion", slug, locale)})
		}
	}
	return sample, total, rows.Err()
}

func (s *pgService) History(ctx context.Context) ([]HistoryItem, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.applied_at, c.applied_by, c.status, c.reverted_at, COALESCE(c.reverted_by, ''), c.plan,
		       (SELECT count(*) FROM hatamoto_locale_conversion_rows WHERE conversion_id = c.id)
		  FROM hatamoto_locale_conversions c ORDER BY c.id DESC`)
	if err != nil {
		return nil, fmt.Errorf("load history: %w", err)
	}
	defer rows.Close()
	out := []HistoryItem{}
	for rows.Next() {
		var h HistoryItem
		var plan []byte
		if err := rows.Scan(&h.ID, &h.AppliedAt, &h.AppliedBy, &h.Status, &h.RevertedAt, &h.RevertedBy, &plan, &h.Rows); err != nil {
			return nil, err
		}
		var p Plan
		if err := json.Unmarshal(plan, &p); err != nil {
			return nil, fmt.Errorf("decode plan of conversion %d: %w", h.ID, err)
		}
		h.Locales = []string{}
		for _, l := range p.Locales {
			h.Locales = append(h.Locales, normalized(l.Tag))
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
