//go:build integration

package siteconfig_test

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/siteconfig"
	"hatamoto.dev/core/migrations"
)

// scratchStore migrates a fresh database next to DATABASE_URL, seeds the
// singleton row and returns a store on it.
func scratchStore(t *testing.T) *siteconfig.Store {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, skipping integration test")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := "hm_siteconfig_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close(ctx)
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	pcfg, err := pgxpool.ParseConfig(u.String())
	if err != nil {
		t.Fatal(err)
	}
	db, err := database.NewWithConfig(ctx, pcfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	var sdb *sql.DB = stdlib.OpenDBFromPool(db.Pool())
	defer sdb.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, sdb, migrations.FS, goose.WithAllowOutofOrder(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.Pool().Exec(ctx, `INSERT INTO hatamoto_site_config (company_name) VALUES ('Example')`); err != nil {
		t.Fatal(err)
	}
	return siteconfig.NewStore(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestLocalesDefaultsAreEmpty(t *testing.T) {
	cfg, err := scratchStore(t).Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Locales != nil || cfg.PrefixAll || cfg.XDefaultLocale != "" {
		t.Errorf("defaults = %#v %v %q, want nil/false/empty", cfg.Locales, cfg.PrefixAll, cfg.XDefaultLocale)
	}
	if cfg.Language != "en" {
		t.Errorf("Language = %q, want en", cfg.Language)
	}
}

func TestLocalesRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := scratchStore(t)
	want := []siteconfig.LocaleEntry{{Tag: "fr", Segment: "fr"}, {Tag: "es-MX", Segment: "mx"}}
	prefix, xdef, company := true, "fr", "Renamed"
	cfg, err := s.Update(ctx, siteconfig.UpdateInput{Locales: &want, PrefixAll: &prefix, XDefaultLocale: &xdef, CompanyName: &company})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Locales, want) || !cfg.PrefixAll || cfg.XDefaultLocale != "fr" {
		t.Fatalf("got %#v %v %q", cfg.Locales, cfg.PrefixAll, cfg.XDefaultLocale)
	}

	// A patch that leaves the locale fields nil keeps them.
	tag := "tagline"
	cfg, err = s.Update(ctx, siteconfig.UpdateInput{Tagline: &tag})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Locales, want) || !cfg.PrefixAll || cfg.XDefaultLocale != "fr" || cfg.CompanyName != company {
		t.Fatalf("nil patch changed locales: %#v %v %q", cfg.Locales, cfg.PrefixAll, cfg.XDefaultLocale)
	}

	// An empty slice clears the list.
	empty, off, none := []siteconfig.LocaleEntry{}, false, ""
	cfg, err = s.Update(ctx, siteconfig.UpdateInput{Locales: &empty, PrefixAll: &off, XDefaultLocale: &none})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Locales != nil || cfg.PrefixAll || cfg.XDefaultLocale != "" {
		t.Fatalf("cleared = %#v %v %q", cfg.Locales, cfg.PrefixAll, cfg.XDefaultLocale)
	}
}

func TestSitemapNamingRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := scratchStore(t)
	cfg, err := s.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SitemapNaming != "group" {
		t.Fatalf("default SitemapNaming = %q, want group", cfg.SitemapNaming)
	}

	legacy, company := "legacy", "Renamed"
	cfg, err = s.Update(ctx, siteconfig.UpdateInput{SitemapNaming: &legacy, CompanyName: &company})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SitemapNaming != "legacy" || cfg.CompanyName != company {
		t.Fatalf("got %q %q", cfg.SitemapNaming, cfg.CompanyName)
	}

	// A nil patch leaves it, and a patch of it leaves other fields.
	tag := "tagline"
	cfg, err = s.Update(ctx, siteconfig.UpdateInput{Tagline: &tag})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SitemapNaming != "legacy" {
		t.Fatalf("nil patch changed SitemapNaming to %q", cfg.SitemapNaming)
	}
	group := "group"
	cfg, err = s.Update(ctx, siteconfig.UpdateInput{SitemapNaming: &group})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SitemapNaming != "group" || cfg.CompanyName != company || cfg.Tagline != tag {
		t.Fatalf("got %q %q %q", cfg.SitemapNaming, cfg.CompanyName, cfg.Tagline)
	}

	// The CHECK constraint refuses anything else.
	bad := "x"
	if _, err := s.Update(ctx, siteconfig.UpdateInput{SitemapNaming: &bad}); err == nil {
		t.Fatal("update with sitemap_naming x succeeded, want CHECK violation")
	}
}
