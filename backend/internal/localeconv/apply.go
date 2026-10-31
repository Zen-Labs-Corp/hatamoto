package localeconv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Apply steps, in order. ApplyOpts.failAfter names one of them.
const (
	stepRename   = "rename"
	stepLocale   = "locale"
	stepHomes    = "homes"
	stepReparent = "reparent"
	stepStrip    = "strip"
	stepSchemas  = "schemas"
	stepSettings = "settings"
	stepRecord   = "record"
)

var errInjectedFailure = errors.New("injected failure")

func stepFailure(opts ApplyOpts, step string) error {
	if opts.failAfter == step {
		return fmt.Errorf("after %s: %w", step, errInjectedFailure)
	}
	return nil
}

func (s *pgService) Apply(ctx context.Context, plan *Plan, opts ApplyOpts) (*Record, *Report, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockWrites(ctx, tx); err != nil {
		return nil, nil, err
	}
	snap, err := loadSnapshot(ctx, tx)
	if err != nil {
		return nil, nil, err
	}
	ch, rep := Analyze(snap, plan)
	if !rep.OK {
		return nil, rep, &BlockingError{Issues: rep.Errors, Report: rep}
	}
	if opts.Fingerprint != "" && opts.Fingerprint != rep.DataFingerprint {
		return nil, rep, &BlockingError{Report: rep, Issues: []Issue{{
			Code: CodeStaleReport, Message: "data changed since the dry run, run it again"}}}
	}

	before, err := readSettings(ctx, tx)
	if err != nil {
		return nil, nil, err
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('hatamoto.import', 'on', true)`); err != nil {
		return nil, nil, fmt.Errorf("set import mode: %w", err)
	}

	rows := ch.Rows()
	groups := newGroupIDs(rows)
	for _, step := range []struct {
		name string
		run  func() error
	}{
		{stepRename, func() error { return writeSlugs(ctx, tx, rows, func(p *Planned) bool { return p.Renamed }) }},
		{stepLocale, func() error { return writeLocales(ctx, tx, rows, groups) }},
		{stepHomes, func() error { return writeSlugs(ctx, tx, rows, func(p *Planned) bool { return p.Home }) }},
		{stepReparent, func() error { return writeParents(ctx, tx, rows) }},
		{stepStrip, func() error { return writeStrips(ctx, tx, rows) }},
		{stepSchemas, func() error { return writeSchemas(ctx, tx, ch.Schemas) }},
		{stepSettings, func() error { return writeSettings(ctx, tx, plan) }},
	} {
		if err := step.run(); err != nil {
			return nil, nil, fmt.Errorf("convert (%s): %w", step.name, err)
		}
		if err := stepFailure(opts, step.name); err != nil {
			return nil, nil, err
		}
	}

	rec, err := insertRecord(ctx, tx, plan, rep, opts, before, ch, rows)
	if err != nil {
		return nil, nil, err
	}
	if err := stepFailure(opts, stepRecord); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("commit: %w", err)
	}
	return rec, rep, nil
}

// newGroupIDs gives each translation group key its uuid, in key order.
func newGroupIDs(rows []*Planned) map[string]string {
	keys := map[string]bool{}
	for _, p := range rows {
		if p.GroupKey != "" {
			keys[p.GroupKey] = true
		}
	}
	out := make(map[string]string, len(keys))
	for _, k := range sortedStrings(keys) {
		out[k] = uuid.NewString()
	}
	return out
}

func writeLocales(ctx context.Context, tx pgx.Tx, rows []*Planned, groups map[string]string) error {
	var ids, locales, grps []string
	for _, p := range rows {
		if p.After.Locale == p.Ent.Locale && p.GroupKey == "" {
			continue
		}
		grp := p.Ent.Group
		if p.GroupKey != "" {
			grp = groups[p.GroupKey]
		}
		ids, locales, grps = append(ids, p.Ent.ID), append(locales, p.After.Locale), append(grps, grp)
	}
	_, err := tx.Exec(ctx, `
		UPDATE hatamoto_entities e SET locale = v.locale, translation_group = NULLIF(v.grp, '')::uuid
		  FROM unnest($1::text[], $2::text[], $3::text[]) AS v(id, locale, grp)
		 WHERE e.id = v.id::uuid`, ids, locales, grps)
	return err
}

func writeSlugs(ctx context.Context, tx pgx.Tx, rows []*Planned, pick func(*Planned) bool) error {
	var ids, slugs []string
	for _, p := range rows {
		if pick(p) && p.After.Slug != p.Ent.Slug {
			ids, slugs = append(ids, p.Ent.ID), append(slugs, p.After.Slug)
		}
	}
	_, err := tx.Exec(ctx, `
		UPDATE hatamoto_entities e SET slug = v.slug
		  FROM unnest($1::text[], $2::text[]) AS v(id, slug) WHERE e.id = v.id::uuid`, ids, slugs)
	return err
}

// writeParents clears parent_id of the children of region homes. Only that
// move exists: every other row keeps its parent.
func writeParents(ctx context.Context, tx pgx.Tx, rows []*Planned) error {
	var ids []string
	for _, p := range rows {
		if p.After.ParentID != p.Ent.ParentID {
			ids = append(ids, p.Ent.ID)
		}
	}
	_, err := tx.Exec(ctx, `UPDATE hatamoto_entities SET parent_id = NULL WHERE id = ANY($1::uuid[])`, ids)
	return err
}

// writeStrips removes strip_attributes keys, one statement per distinct key set.
func writeStrips(ctx context.Context, tx pgx.Tx, rows []*Planned) error {
	bySet := map[string][]string{}
	for _, p := range rows {
		if len(p.Strip) > 0 {
			k := strings.Join(p.Strip, "\x00")
			bySet[k] = append(bySet[k], p.Ent.ID)
		}
	}
	for _, k := range sortedStrings(bySet) {
		if _, err := tx.Exec(ctx, `UPDATE hatamoto_entities SET attributes = attributes - $2::text[] WHERE id = ANY($1::uuid[])`,
			bySet[k], strings.Split(k, "\x00")); err != nil {
			return err
		}
	}
	return nil
}

func writeSchemas(ctx context.Context, tx pgx.Tx, changes []SchemaChange) error {
	for _, c := range changes {
		if _, err := tx.Exec(ctx, `UPDATE hatamoto_entity_schemas SET settings = $2::jsonb WHERE id = $1::uuid`, c.ID, []byte(c.After)); err != nil {
			return fmt.Errorf("schema %s: %w", c.Slug, err)
		}
	}
	return nil
}

// writeSettings sets the locale settings in one UPDATE and clears the explicit
// homepage, which would otherwise win over every locale home. With two or more
// locales the retag trigger returns without touching entities.
func writeSettings(ctx context.Context, tx pgx.Tx, plan *Plan) error {
	current, err := readSettings(ctx, tx)
	if err != nil {
		return err
	}
	list, err := plannedSiteLocales(current.Locales, plan)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(list)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE hatamoto_site_config
		   SET language = $1, locales = $2::jsonb, prefix_all = $3, x_default_locale = $4,
		       homepage_entity_id = NULL
		 WHERE id = TRUE`, normalized(plan.Default), raw, plan.PrefixAllValue(), normalized(plan.XDefault))
	return err
}

// plannedSiteLocales is the list the plan stores: its tags and segments, with
// the hidden flag and display name of every tag that is already stored.
func plannedSiteLocales(stored []byte, plan *Plan) ([]SiteLocale, error) {
	var old []SiteLocale
	if len(stored) > 0 {
		if err := json.Unmarshal(stored, &old); err != nil {
			return nil, fmt.Errorf("read stored locales: %w", err)
		}
	}
	kept := make(map[string]SiteLocale, len(old))
	for _, l := range old {
		kept[normalized(l.Tag)] = l
	}
	list := make([]SiteLocale, len(plan.Locales))
	for i, l := range plan.Locales {
		tag := normalized(l.Tag)
		list[i] = SiteLocale{Tag: tag, Segment: l.Segment, Hidden: kept[tag].Hidden, Name: kept[tag].Name}
	}
	return list, nil
}

// schemaRecord is one entry of schemas_before / schemas_after.
type schemaRecord struct {
	ID       string          `json:"id"`
	Settings json.RawMessage `json:"settings"`
}

func insertRecord(ctx context.Context, tx pgx.Tx, plan *Plan, rep *Report, opts ApplyOpts, before settingsJSON, ch *Changes, rows []*Planned) (*Record, error) {
	after, err := readSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	var schemasBefore, schemasAfter []schemaRecord
	for _, c := range ch.Schemas {
		schemasBefore = append(schemasBefore, schemaRecord{ID: c.ID, Settings: c.Before})
		schemasAfter = append(schemasAfter, schemaRecord{ID: c.ID, Settings: c.After})
	}
	planJSON, _ := json.Marshal(plan)
	reportJSON, _ := json.Marshal(rep)
	args := make([][]byte, 0, 6)
	for _, v := range []any{before, after, orEmptyList(schemasBefore), orEmptyList(schemasAfter)} {
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		args = append(args, raw)
	}

	rec := &Record{AppliedBy: opts.By, Rows: len(rows)}
	if err := tx.QueryRow(ctx, `
		INSERT INTO hatamoto_locale_conversions (applied_by, plan, report, settings_before, settings_after, schemas_before, schemas_after)
		VALUES ($1, $2::jsonb, $3::jsonb, $4::jsonb, $5::jsonb, $6::jsonb, $7::jsonb)
		RETURNING id, applied_at, status`,
		opts.By, planJSON, reportJSON, args[0], args[1], args[2], args[3]).Scan(&rec.ID, &rec.AppliedAt, &rec.Status); err != nil {
		return nil, fmt.Errorf("record conversion: %w", err)
	}

	// The after-values are read back, so the record holds what is stored (the
	// import flag kept updated_at, and the read gives its value at commit).
	ids := make([]string, len(rows))
	byID := make(map[string]*Planned, len(rows))
	for i, p := range rows {
		ids[i], byID[p.Ent.ID] = p.Ent.ID, p
	}
	cur, err := tx.Query(ctx, `
		SELECT id::text, locale, slug, COALESCE(parent_id::text, ''), COALESCE(translation_group::text, ''), COALESCE(updated_at, created_at, 'epoch')
		  FROM hatamoto_entities WHERE id = ANY($1::uuid[]) ORDER BY id`, ids)
	if err != nil {
		return nil, fmt.Errorf("read back rows: %w", err)
	}
	type recRow struct {
		id, bLocale, bSlug, bParent, bGroup, bAttrs string
		aLocale, aSlug, aParent, aGroup             string
		aUpdated                                    time.Time
	}
	var recs []recRow
	for cur.Next() {
		var r recRow
		if err := cur.Scan(&r.id, &r.aLocale, &r.aSlug, &r.aParent, &r.aGroup, &r.aUpdated); err != nil {
			cur.Close()
			return nil, err
		}
		p := byID[r.id]
		r.bLocale, r.bSlug, r.bParent, r.bGroup = p.Ent.Locale, p.Ent.Slug, p.Ent.ParentID, p.Ent.Group
		if r.bAttrs, err = strippedAttrs(p); err != nil {
			cur.Close()
			return nil, err
		}
		recs = append(recs, r)
	}
	cur.Close()
	if err := cur.Err(); err != nil {
		return nil, err
	}
	cols := make([][]string, 10)
	updated := make([]time.Time, 0, len(recs))
	for _, r := range recs {
		for i, v := range []string{r.id, r.bLocale, r.bSlug, r.bParent, r.bGroup, r.bAttrs, r.aLocale, r.aSlug, r.aParent, r.aGroup} {
			cols[i] = append(cols[i], v)
		}
		updated = append(updated, r.aUpdated)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO hatamoto_locale_conversion_rows
		  (conversion_id, entity_id, before_locale, before_slug, before_parent_id, before_group, before_attrs,
		   after_locale, after_slug, after_parent_id, after_group, after_updated_at)
		SELECT $1, v.id::uuid, v.bl, v.bs, NULLIF(v.bp, '')::uuid, NULLIF(v.bg, '')::uuid, NULLIF(v.ba, '')::jsonb,
		       v.al, v.asl, NULLIF(v.ap, '')::uuid, NULLIF(v.ag, '')::uuid, v.au
		  FROM unnest($2::text[], $3::text[], $4::text[], $5::text[], $6::text[], $7::text[], $8::text[], $9::text[], $10::text[], $11::text[], $12::timestamptz[])
		       AS v(id, bl, bs, bp, bg, ba, al, asl, ap, ag, au)`,
		rec.ID, cols[0], cols[1], cols[2], cols[3], cols[4], cols[5], cols[6], cols[7], cols[8], cols[9], updated); err != nil {
		return nil, fmt.Errorf("record rows: %w", err)
	}
	return rec, nil
}

func orEmptyList(l []schemaRecord) []schemaRecord {
	if l == nil {
		return []schemaRecord{}
	}
	return l
}

// strippedAttrs is the JSON object of the attribute keys the conversion removes
// from the row ("" when none), with their stored values.
func strippedAttrs(p *Planned) (string, error) {
	if len(p.Strip) == 0 {
		return "", nil
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(p.Ent.Attrs, &all); err != nil {
		return "", fmt.Errorf("attributes of %s: %w", p.Ent.ID, err)
	}
	kept := map[string]json.RawMessage{}
	for _, k := range p.Strip {
		kept[k] = all[k]
	}
	raw, err := json.Marshal(kept)
	return string(raw), err
}
