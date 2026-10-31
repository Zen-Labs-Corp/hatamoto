//go:build integration

package taxonomy

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/migrations"
)

// groupFixture is a migrated scratch database with a term service wired to a
// TermGroupStore.
type groupFixture struct {
	svc    *Service
	groups *TermGroupStore
	db     *database.DB
}

func newGroupFixture(t *testing.T) *groupFixture {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := "hm_tg_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create scratch db: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		admin.Close()
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	sqlDB, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS, goose.WithAllowOutofOrder(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	_ = sqlDB.Close()

	db, err := database.New(ctx, config.DatabaseConfig{URL: u.String(), MaxConnections: 4, MinConnections: 1, MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	svc := NewService(NewPostgresRepository(db), nil)
	groups := NewTermGroupStore(db)
	svc.SetGroupStore(groups)
	return &groupFixture{svc: svc, groups: groups, db: db}
}

func (f *groupFixture) term(t *testing.T, kind, slug, locale string) *Term {
	t.Helper()
	ctx := context.Background()
	tm, err := f.svc.CreateTerm(ctx, CreateTermInput{Kind: kind, Name: slug, Slug: slug})
	if err != nil {
		t.Fatal(err)
	}
	if locale != "" {
		if err := f.groups.SetLocale(ctx, tm.ID, locale); err != nil {
			t.Fatal(err)
		}
	}
	return tm
}

func (f *groupFixture) count(t *testing.T, kind string) int {
	t.Helper()
	var n int
	if err := f.db.Pool().QueryRow(context.Background(), `SELECT count(*) FROM hatamoto_terms WHERE kind = $1`, kind).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTermGroupLinkRules(t *testing.T) {
	f := newGroupFixture(t)
	ctx := context.Background()
	en := f.term(t, "category", "guides", "en-US")
	fr := f.term(t, "category", "guides-fr", "fr-FR")
	fr2 := f.term(t, "category", "guides-fr2", "fr-FR")
	de := f.term(t, "category", "guides-de", "de-DE")
	tag := f.term(t, "tag", "tag-fr", "it-IT")
	none := f.term(t, "category", "no-locale", "")

	if _, err := f.groups.Link(ctx, en.ID); !errors.Is(err, ErrTermGroupInvalid) {
		t.Errorf("one id: %v", err)
	}
	if _, err := f.groups.Link(ctx, en.ID, fr2.ID, fr.ID); !errors.Is(err, ErrTermLocaleTaken) {
		t.Errorf("duplicate locale: %v", err)
	}
	if _, err := f.groups.Link(ctx, en.ID, tag.ID); !errors.Is(err, ErrTermGroupInvalid) {
		t.Errorf("mixed kind: %v", err)
	}
	if _, err := f.groups.Link(ctx, en.ID, none.ID); !errors.Is(err, ErrTermGroupInvalid) {
		t.Errorf("no locale: %v", err)
	}
	if err := f.groups.SetLocale(ctx, en.ID, "und"); !errors.Is(err, ErrTermGroupInvalid) {
		t.Errorf("und locale: %v", err)
	}

	g, err := f.groups.Link(ctx, en.ID, fr.ID)
	if err != nil || g == uuid.Nil {
		t.Fatalf("link: %v", err)
	}
	if _, err := f.groups.Link(ctx, en.ID, fr2.ID); !errors.Is(err, ErrTermLocaleTaken) {
		t.Errorf("join taken locale: %v", err)
	}
	if g2, err := f.groups.Link(ctx, fr.ID, de.ID); err != nil || g2 != g {
		t.Errorf("join existing group: %v, %v vs %v", g2, err, g)
	}
	if err := f.groups.SetLocale(ctx, de.ID, "fr-FR"); !errors.Is(err, ErrTermLocaleTaken) {
		t.Errorf("set taken locale: %v", err)
	}
	if err := f.groups.SetLocale(ctx, de.ID, ""); !errors.Is(err, ErrTermGroupInvalid) {
		t.Errorf("clear locale of a linked term: %v", err)
	}
	m, err := f.groups.Members(ctx, fr.ID)
	if err != nil || len(m) != 3 || m[0].Locale != "de-DE" {
		t.Fatalf("members = %+v, %v", m, err)
	}

	// Unlink leaves a 2-member group, then dissolves it at one member.
	if err := f.groups.Unlink(ctx, de.ID); err != nil {
		t.Fatal(err)
	}
	if m, _ := f.groups.Members(ctx, en.ID); len(m) != 2 {
		t.Fatalf("members after first unlink = %+v", m)
	}
	if err := f.groups.Unlink(ctx, en.ID); err != nil {
		t.Fatal(err)
	}
	if m, _ := f.groups.Members(ctx, fr.ID); m != nil {
		t.Errorf("one-member group still reported: %+v", m)
	}
	if loc, _ := f.groups.Locale(ctx, en.ID); loc != "en-US" {
		t.Errorf("unlink changed the locale to %q", loc)
	}
}

func TestTermGroupMapForLocale(t *testing.T) {
	f := newGroupFixture(t)
	ctx := context.Background()
	en := f.term(t, "category", "guides", "en-US")
	fr := f.term(t, "category", "guides-fr", "fr-FR")
	other := f.term(t, "category", "news", "en-US")
	plain := f.term(t, "category", "plain", "")
	if _, err := f.groups.Link(ctx, en.ID, fr.ID); err != nil {
		t.Fatal(err)
	}
	got, err := f.groups.MapForLocale(ctx, []uuid.UUID{en.ID, other.ID, plain.ID, fr.ID}, "fr-FR")
	if err != nil {
		t.Fatal(err)
	}
	want := []uuid.UUID{fr.ID, other.ID, plain.ID}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestTermGroupCandidates(t *testing.T) {
	f := newGroupFixture(t)
	ctx := context.Background()
	en := f.term(t, "category", "guides", "en-US")
	fr := f.term(t, "category", "guides-fr", "fr-FR")
	f.term(t, "category", "guides-fr-b", "fr-FR")
	de := f.term(t, "category", "guides-de", "de-DE")
	f.term(t, "tag", "guides-tag", "es-ES")
	if _, err := f.groups.Link(ctx, en.ID, fr.ID); err != nil {
		t.Fatal(err)
	}
	got, err := f.groups.Candidates(ctx, en.ID, "guides", 10)
	if err != nil || len(got) != 1 || got[0].ID != de.ID {
		t.Fatalf("candidates = %+v, %v; want only the de-DE category", got, err)
	}
}

func TestCreateTermCopy(t *testing.T) {
	f := newGroupFixture(t)
	ctx := context.Background()
	parent := f.term(t, "category", "guides", "en-US")
	parentFR := f.term(t, "category", "guides-fr", "fr-FR")
	if _, err := f.groups.Link(ctx, parent.ID, parentFR.ID); err != nil {
		t.Fatal(err)
	}
	child, err := f.svc.CreateTerm(ctx, CreateTermInput{Kind: "category", Name: "Setup", Slug: "setup", ParentID: &parent.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CreateTermCopy(ctx, child.ID, "fr-FR", "", "setup-fr"); !errors.Is(err, ErrTermLocaleUnset) {
		t.Fatalf("copy of a locale-less term: %v", err)
	}
	if err := f.groups.SetLocale(ctx, child.ID, "en-US"); err != nil {
		t.Fatal(err)
	}
	cp, err := f.svc.CreateTermCopy(ctx, child.ID, "fr-FR", "Installation", "setup-fr")
	if err != nil {
		t.Fatal(err)
	}
	if cp.Name != "Installation" || cp.ParentID == nil || *cp.ParentID != parentFR.ID {
		t.Fatalf("copy = %+v, want Installation under the fr-FR parent", cp)
	}
	if loc, _ := f.groups.Locale(ctx, cp.ID); loc != "fr-FR" {
		t.Errorf("copy locale = %q", loc)
	}
	if m, _ := f.groups.Members(ctx, child.ID); len(m) != 2 {
		t.Errorf("members = %+v", m)
	}
	if _, err := f.svc.CreateTermCopy(ctx, child.ID, "fr-FR", "Again", "setup-fr-2"); !errors.Is(err, ErrTermLocaleTaken) {
		t.Errorf("second fr-FR copy: %v", err)
	}
	if _, err := f.svc.CreateTermCopy(ctx, child.ID, "und", "x", "x"); !errors.Is(err, ErrTermGroupInvalid) {
		t.Errorf("und copy: %v", err)
	}
}

// A slug clash or a failed link leaves no new term behind.
func TestCreateTermCopyLeavesNoOrphan(t *testing.T) {
	f := newGroupFixture(t)
	ctx := context.Background()
	src := f.term(t, "category", "guides", "en-US")
	f.term(t, "category", "taken", "")
	before := f.count(t, "category")

	if _, err := f.svc.CreateTermCopy(ctx, src.ID, "fr-FR", "Guides fr", "taken"); !errors.Is(err, ErrTermSlugConflict) {
		t.Fatalf("slug clash: %v", err)
	}
	// Make the link fail after the term exists.
	if _, err := f.db.Pool().Exec(ctx, `
		CREATE OR REPLACE FUNCTION fail_term_link() RETURNS trigger AS $$
		BEGIN RAISE EXCEPTION 'forced link failure'; END $$ LANGUAGE plpgsql;
		CREATE TRIGGER fail_term_link BEFORE UPDATE OF translation_group ON hatamoto_terms
		FOR EACH ROW EXECUTE FUNCTION fail_term_link()`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CreateTermCopy(ctx, src.ID, "fr-FR", "Guides fr", "guides-fr"); err == nil {
		t.Fatal("expected the forced link failure")
	}
	if got := f.count(t, "category"); got != before {
		t.Errorf("categories = %d, want %d: a copy was left behind", got, before)
	}
}

// Deleting a term leaves its partner in a group of one. That counts as no
// group: the survivor can be linked elsewhere and is offered as a candidate.
func TestTermGroupOneMemberCountsAsUngrouped(t *testing.T) {
	f := newGroupFixture(t)
	ctx := context.Background()
	en := f.term(t, "category", "guides", "en-US")
	fr := f.term(t, "category", "guides-fr", "fr-FR")
	pt := f.term(t, "category", "guias", "pt-BR")
	de := f.term(t, "category", "guides-de", "de-DE")
	if _, err := f.groups.Link(ctx, en.ID, fr.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.DeleteTerm(ctx, fr.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.groups.Link(ctx, pt.ID, de.ID); err != nil {
		t.Fatal(err)
	}
	got, err := f.groups.Candidates(ctx, de.ID, "guides", 10)
	if err != nil || len(got) != 1 || got[0].ID != en.ID {
		t.Fatalf("candidates of a grouped term = %+v, %v; want the survivor %s", got, err, en.ID)
	}
	if _, err := f.groups.Link(ctx, de.ID, en.ID); err != nil {
		t.Fatalf("link the survivor into a real group: %v", err)
	}
	if ms, err := f.groups.Members(ctx, en.ID); err != nil || len(ms) != 3 {
		t.Errorf("members = %+v, %v; want en-US, pt-BR and de-DE", ms, err)
	}
}
