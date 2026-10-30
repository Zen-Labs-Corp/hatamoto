//go:build integration

package localeconv

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// recordedDump is exactly what the revert guarantee covers: every entity column
// (updated_at included), the schemas' settings and the locale settings.
func recordedDump(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	ctx := context.Background()
	var entities, schemas, site string
	if err := pool.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(e) ORDER BY e.id), '[]')::text FROM hatamoto_entities e`).Scan(&entities); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object('slug', s.slug, 'settings', s.settings) ORDER BY s.slug), '[]')::text FROM hatamoto_entity_schemas s`).Scan(&schemas); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT jsonb_build_object('language', language, 'locales', locales, 'prefix_all', prefix_all, 'x_default_locale', x_default_locale)::text
		FROM hatamoto_site_config WHERE id = TRUE`).Scan(&site); err != nil {
		t.Fatal(err)
	}
	return entities + "\n" + schemas + "\n" + site
}

// stripPlan also strips the "lang" attribute, so the revert has attributes to merge back.
func stripPlan(t *testing.T) *Plan {
	return fixturePlan(t, func(p *Plan) { p.StripAttributes = []string{"lang"} })
}

// converted seeds the fixture, applies the plan and returns the pool, the
// service, the dump before and the record id.
func converted(t *testing.T) (*pgxpool.Pool, Service, string, int64) {
	t.Helper()
	pool := scratchPool(t)
	seedFixture(t, pool, fixtureSnap())
	before := recordedDump(t, pool)
	svc := testService(pool)
	rec, _ := mustApply(t, svc, stripPlan(t))
	if recordedDump(t, pool) == before {
		t.Fatal("apply changed nothing")
	}
	var lang *string
	if err := pool.QueryRow(context.Background(), `SELECT attributes->>'lang' FROM hatamoto_entities WHERE id = $1`, uid("x0")).Scan(&lang); err != nil || lang != nil {
		t.Fatalf("strip_attributes left lang = %v (%v)", lang, err)
	}
	return pool, svc, before, rec.ID
}

func refusal(t *testing.T, err error) []Issue {
	t.Helper()
	var be *BlockingError
	if !errors.As(err, &be) {
		t.Fatalf("err = %v, want a BlockingError", err)
	}
	return be.Issues
}

func TestRevertRestoresUntouchedDataByteForByte(t *testing.T) {
	ctx := context.Background()
	pool, svc, before, id := converted(t)

	res, err := svc.Revert(ctx, id, false, "tester")
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	if res.ConversionID != id || res.Restored != 12 || len(res.Forced) != 0 || res.Stranded != 0 {
		t.Errorf("result = %+v", res)
	}
	if got := recordedDump(t, pool); got != before {
		t.Errorf("revert did not restore the recorded columns:\nbefore %s\nafter  %s", before, got)
	}
	var status, by string
	var at *string
	if err := pool.QueryRow(ctx, `SELECT status, COALESCE(reverted_by, ''), reverted_at::text FROM hatamoto_locale_conversions WHERE id = $1`, id).Scan(&status, &by, &at); err != nil {
		t.Fatal(err)
	}
	if status != "reverted" || by != "tester" || at == nil {
		t.Errorf("record = %q %q %v", status, by, at)
	}

	// A second revert is refused for good, force or not.
	for _, force := range []bool{false, true} {
		_, err = svc.Revert(ctx, id, force, "tester")
		if issues := refusal(t, err); len(issues) != 1 || issues[0].Code != CodeAlreadyReverted {
			t.Errorf("force=%v: issues = %+v", force, issues)
		}
	}
}

func TestRevertRefusesAnEditedRowAndForceRestoresIt(t *testing.T) {
	ctx := context.Background()
	pool, svc, _, id := converted(t)
	// An editor saves y1 (the save bumps updated_at).
	if _, err := pool.Exec(ctx, `UPDATE hatamoto_entities SET name = 'edited' WHERE id = $1`, uid("y1")); err != nil {
		t.Fatal(err)
	}
	afterEdit := recordedDump(t, pool)

	_, err := svc.Revert(ctx, id, false, "tester")
	issues := refusal(t, err)
	if len(issues) != 1 || issues[0].Code != CodeRevertConflict || issues[0].EntityID != uid("y1") || !strings.Contains(issues[0].Message, "edited") {
		t.Fatalf("issues = %+v, want one naming y1", issues)
	}
	if recordedDump(t, pool) != afterEdit {
		t.Fatal("a refused revert changed the database")
	}

	res, err := svc.Revert(ctx, id, true, "tester")
	if err != nil {
		t.Fatalf("forced revert: %v", err)
	}
	if len(res.Forced) != 1 || res.Forced[0].EntityID != uid("y1") {
		t.Errorf("forced = %+v", res.Forced)
	}
	var locale, name string
	if err := pool.QueryRow(ctx, `SELECT locale, name FROM hatamoto_entities WHERE id = $1`, uid("y1")).Scan(&locale, &name); err != nil {
		t.Fatal(err)
	}
	if locale != "en" || name != "edited" {
		t.Errorf("y1 = %q %q: the recorded columns are restored, the edit stays", locale, name)
	}
}

func TestRevertRefusesChangedSettingsAndSchemas(t *testing.T) {
	ctx := context.Background()
	for name, change := range map[string]string{
		"settings": `UPDATE hatamoto_site_config SET x_default_locale = ''`,
		"schema":   `UPDATE hatamoto_entity_schemas SET settings = settings || '{"archive_title":"x"}'::jsonb WHERE slug = 'page'`,
	} {
		t.Run(name, func(t *testing.T) {
			pool, svc, _, id := converted(t)
			if _, err := pool.Exec(ctx, change); err != nil {
				t.Fatal(err)
			}
			dumpBefore := recordedDump(t, pool)
			_, err := svc.Revert(ctx, id, false, "tester")
			if issues := refusal(t, err); len(issues) != 1 || issues[0].Code != CodeRevertConflict {
				t.Fatalf("issues = %+v", issues)
			}
			if recordedDump(t, pool) != dumpBefore {
				t.Error("a refused revert changed the database")
			}
			if _, err := svc.Revert(ctx, id, true, "tester"); err != nil {
				t.Errorf("forced revert: %v", err)
			}
		})
	}
}

func TestRevertListsRowsCreatedSinceAndLeavesThem(t *testing.T) {
	ctx := context.Background()
	pool, svc, _, id := converted(t)
	// Two rows an editor created after the conversion, one in a converted locale.
	for _, row := range []struct{ id, slug, locale string }{{"n1", "fresh", "en-ZZ"}, {"n2", "fresh-shared", "und"}} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO hatamoto_entities (id, schema_id, name, slug, status, locale, created_at)
			VALUES ($1, (SELECT id FROM hatamoto_entity_schemas WHERE slug = 'page'), $2, $2, 'draft', $3, now() + interval '1 second')`,
			uid(row.id), row.slug, row.locale); err != nil {
			t.Fatal(err)
		}
	}

	_, err := svc.Revert(ctx, id, false, "tester")
	issues := refusal(t, err)
	if len(issues) != 1 || issues[0].EntityID != uid("n1") || !strings.Contains(issues[0].Message, "created") {
		t.Fatalf("issues = %+v, want only the row in a converted locale", issues)
	}
	res, err := svc.Revert(ctx, id, true, "tester")
	if err != nil {
		t.Fatalf("forced revert: %v", err)
	}
	if res.Stranded != 1 || len(res.StrandedItems) != 1 || res.StrandedItems[0].EntityID != uid("n1") {
		t.Errorf("stranded = %d %+v", res.Stranded, res.StrandedItems)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM hatamoto_entities WHERE id = ANY($1::uuid[])`, idsOf("n1", "n2")).Scan(&n); err != nil || n != 2 {
		t.Errorf("rows created since: %d left (%v), want both", n, err)
	}
}

func TestRevertReportsADeletedRow(t *testing.T) {
	ctx := context.Background()
	pool, svc, _, id := converted(t)
	if _, err := pool.Exec(ctx, `DELETE FROM hatamoto_entities WHERE id = $1`, uid("z2")); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Revert(ctx, id, false, "tester")
	if issues := refusal(t, err); len(issues) != 1 || issues[0].EntityID != uid("z2") || !strings.Contains(issues[0].Message, "deleted") {
		t.Fatalf("issues = %+v", issues)
	}
	res, err := svc.Revert(ctx, id, true, "tester")
	if err != nil || res.Restored != 11 {
		t.Errorf("forced revert = %+v, %v; want 11 rows restored", res, err)
	}
}

func TestRevertRefusesWhileANewerConversionIsApplied(t *testing.T) {
	ctx := context.Background()
	pool, svc, _, id := converted(t)
	var newer int64
	if err := pool.QueryRow(ctx, `INSERT INTO hatamoto_locale_conversions (plan, report, settings_before, settings_after)
		VALUES ('{}', '{}', '{}', '{}') RETURNING id`).Scan(&newer); err != nil {
		t.Fatal(err)
	}
	for _, force := range []bool{false, true} {
		_, err := svc.Revert(ctx, id, force, "tester")
		if issues := refusal(t, err); len(issues) != 1 || issues[0].Code != CodeNewerConversion {
			t.Errorf("force=%v: issues = %+v", force, issues)
		}
	}
	if _, err := svc.Revert(ctx, 999999, false, "tester"); !errors.Is(err, ErrConversionNotFound) {
		t.Errorf("unknown id: %v", err)
	}
}

func TestHistoryListsNewestFirst(t *testing.T) {
	ctx := context.Background()
	pool, svc, _, id := converted(t)
	if _, err := svc.Revert(ctx, id, false, "tester"); err != nil {
		t.Fatal(err)
	}
	rec2, _ := mustApply(t, svc, stripPlan(t))

	hist, err := svc.History(ctx)
	if err != nil || len(hist) != 2 {
		t.Fatalf("history = %+v, %v", hist, err)
	}
	if hist[0].ID != rec2.ID || hist[0].Status != "applied" || hist[0].RevertedAt != nil || hist[0].Rows != 12 {
		t.Errorf("newest = %+v", hist[0])
	}
	if hist[1].ID != id || hist[1].Status != "reverted" || hist[1].RevertedAt == nil || hist[1].RevertedBy != "tester" {
		t.Errorf("older = %+v", hist[1])
	}
	if len(hist[0].Locales) != 3 || hist[0].Locales[0] != "en-XX" || hist[0].AppliedBy != "cli" {
		t.Errorf("locales/by = %+v", hist[0])
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM hatamoto_locale_conversions`).Scan(&n); err != nil || n != 2 {
		t.Errorf("records = %d (%v)", n, err)
	}
}
