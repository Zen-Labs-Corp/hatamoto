//go:build integration

package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/adminpath"
)

func migratedPool(t *testing.T) (context.Context, *sqlSite) {
	t.Helper()
	pool, ctx := scratchPool(t), context.Background()
	if err := migrateUp(ctx, pool, quietLogger()); err != nil {
		t.Fatal(err)
	}
	if err := firstBoot(ctx, pool, "Example", quietLogger()); err != nil {
		t.Fatal(err)
	}
	return ctx, newSQLSite(pool, "Example", "https://example.com")
}

func TestSQLSiteReservedBySlugAndRedirect(t *testing.T) {
	ctx, site := migratedPool(t)
	pool := site.pool
	if _, err := pool.Exec(ctx, `INSERT INTO hatamoto_redirects (source_path, target_url) VALUES ('/old-shop', '/p/new'), ('/dead-link', '/p/x'), ('/shop/old-item', '/p/new'), ('/gone-section/**', '/p/new')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE hatamoto_redirects SET is_active = false WHERE source_path = '/dead-link'`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE hatamoto_entity_schemas SET settings = '{"url_pattern":"/team/{slug}","archive_path":"/crew"}' WHERE slug = 'page'`); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/home", "/old-shop", "/team", "/crew", "/shop", "/gone-section"} {
		if res := adminpath.Assess(ctx, path, site); res.Level != adminpath.LevelReserved {
			t.Errorf("%s = %v %v, want reserved", path, res.Level, res.Reasons)
		}
	}
	if res := adminpath.Assess(ctx, "/dead-link", site); res.Level == adminpath.LevelReserved {
		t.Error("an inactive redirect must not reserve its address")
	}
	if res := adminpath.Assess(ctx, "/k7qm-x2rf-9tdc", site); res.Level != adminpath.LevelStrong {
		t.Errorf("free address = %v", res.Level)
	}
}

func TestAdminURLEndToEnd(t *testing.T) {
	ctx, site := migratedPool(t)
	pool := site.pool
	st := adminpath.NewStore(pool)
	env := func(string) string { return "" }
	base := func(context.Context) string { return "https://example.com" }
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := runAdminURL(ctx, st, site, args, env, base, &out)
		return out.String(), err
	}

	if out, err := run(); err != nil || !strings.Contains(out, "not chosen yet") {
		t.Fatalf("show before boot: %q, %v", out, err)
	}
	if _, err := resolveAdminPath(ctx, st, site, owner(false), "", quietLogger()); err != nil {
		t.Fatal(err)
	}
	stored, _ := st.Get(ctx)
	if out, err := run(); err != nil || out != "https://example.com"+*stored.Path+"\n" {
		t.Fatalf("show: %q, %v", out, err)
	}

	out, err := run("set", "/admin")
	var ee exitError
	if !errors.As(err, &ee) || !strings.HasPrefix(out, "refused: ") {
		t.Fatalf("weak set: %q, %v", out, err)
	}
	if now, _ := st.Get(ctx); *now.Path != *stored.Path {
		t.Error("refused set changed the address")
	}
	if out, err = run("set", "/home"); !errors.As(err, &ee) || !strings.Contains(out, "reserved") {
		t.Fatalf("slug set: %q, %v", out, err)
	}

	if out, err = run("set", "/admin", "--i-understand-weak"); err != nil || !strings.Contains(out, "(weak, acknowledged by cli at ") || !strings.HasSuffix(out, "restart hatamoto to apply\n") {
		t.Fatalf("weak set: %q, %v", out, err)
	}
	if got, _ := st.Get(ctx); *got.Path != "/admin" || got.AckBy == nil || *got.AckBy != "cli" || got.AckAt == nil {
		t.Fatalf("after weak set: %+v", got)
	}

	if out, err = run("random"); err != nil || !strings.Contains(out, "(strong)") {
		t.Fatalf("random: %q, %v", out, err)
	}
	if got, _ := st.Get(ctx); *got.Path == "/admin" || got.AckBy != nil || got.AckAt != nil {
		t.Fatalf("after random: %+v", got)
	}
}
