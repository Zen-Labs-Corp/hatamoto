//go:build integration

package wpimport

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

// Two posts that share a slug in English and French are two entities, each
// tagged with its language, with no -2 suffix. The French one is stored but
// not public, so it gets no redirect.
func TestImport_SameSlugTwoLanguages(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1, MaxConnLifetime: time.Hour, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	d := NewDeps(db, DepsConfig{MediaDir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(d.Close)
	pool := db.Pool()

	// A database that never booted has no site row; the importer needs one.
	if tag, err := pool.Exec(ctx, `INSERT INTO hatamoto_site_config (company_name) VALUES ('wpl test') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	} else if tag.RowsAffected() == 1 {
		t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM hatamoto_site_config WHERE company_name = 'wpl test'`) })
	}
	site, err := d.Site.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(strings.SplitN(site.Language, "-", 2)[0], "en") {
		t.Skipf("test database site language is %q, the test needs an English site", site.Language)
	}

	const host = "wpl.example.test"
	cleanup := func() {
		_, _ = pool.Exec(ctx, `DELETE FROM hatamoto_entities WHERE metadata->>'import_key' LIKE 'wp:`+host+`:%'`)
		_, _ = pool.Exec(ctx, `DELETE FROM hatamoto_redirects WHERE source_path LIKE '/wpl-%'`)
	}
	cleanup()
	t.Cleanup(cleanup)

	d0, err := ParseWXR(strings.NewReader(strings.NewReplacer("multi.example", host, "hello", "wpl-hello").Replace(wpmlWXR)), WXROptions{})
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRunner(d, NewStore(pool, "https://"+host), d0, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// The phases that matter, without the settings step: the shared test
	// database keeps its own title, tagline and language.
	if r.run, err = r.st.StartRun(ctx, RunRunning, nil); err != nil {
		t.Fatal(err)
	}
	r.state = &SiteState{Mode: ModeExisting}
	for _, step := range []func(context.Context) error{r.prepareSchemas, r.importContent, r.importRedirects} {
		if err := step(ctx); err != nil {
			t.Fatal(err)
		}
	}
	res := r.res
	if res.Failed != 0 || len(res.Errors) > 0 {
		t.Fatalf("failed=%d errors=%+v", res.Failed, res.Errors)
	}

	rows, err := pool.Query(ctx, `SELECT slug, locale, metadata->'wordpress'->>'id' FROM hatamoto_entities WHERE metadata->>'import_key' LIKE 'wp:`+host+`:%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{} // wordpress id -> slug/locale
	for rows.Next() {
		var slug, loc, id string
		if err := rows.Scan(&slug, &loc, &id); err != nil {
			t.Fatal(err)
		}
		got[id] = slug + "/" + loc
	}
	want := map[string]string{"1": "wpl-hello/" + site.Language, "2": "wpl-hello/fr", "3": "salut/fr", "4": "plain/" + site.Language}
	if len(got) != len(want) {
		t.Fatalf("entities %v, want %v", got, want)
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("post %s stored as %q, want %q", id, got[id], w)
		}
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM hatamoto_redirects WHERE source_path = '/fr/wpl-hello/'`).Scan(&n)
	if n != 0 {
		t.Errorf("a redirect was made for the French post, which is not public")
	}
	reported := false
	for _, w := range res.Warnings {
		reported = reported || strings.Contains(w, "other languages are stored but not public")
	}
	if !reported {
		t.Errorf("warnings do not report the non-public items: %v", res.Warnings)
	}
}
