//go:build integration

package wpimport

import (
	"context"
	"io"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/commentfmt"
	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
)

const commentsTestSecret = "integration-test-secret"

// A WXR import brings the comments over silently: statuses, threading, hashed
// commenters, no URL or IP, comments stay off, and a re-run changes nothing.
func TestImportComments_Integration(t *testing.T) {
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
	d := NewDeps(db, DepsConfig{MediaDir: t.TempDir(), Secret: commentsTestSecret}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(d.Close)
	pool := db.Pool()
	keys := comment.NewKeys(commentsTestSecret)

	if tag, err := pool.Exec(ctx, `INSERT INTO hatamoto_site_config (company_name) VALUES ('wpc test') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	} else if tag.RowsAffected() == 1 {
		t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM hatamoto_site_config WHERE company_name = 'wpc test'`) })
	}

	const host = "wpc.example.test"
	emails := []string{"ann", "bob", "cy", "dee", "spam", "gone", "gone2", "orphan", "rita", "nodate", "pre"}
	var hashes []string
	for _, e := range emails {
		hashes = append(hashes, keys.HashEmail(e+"@example.test"))
	}
	cleanup := func() {
		_, _ = pool.Exec(ctx, `DELETE FROM hatamoto_entities WHERE metadata->>'import_key' LIKE 'wp:`+host+`:%' OR slug = 'wpc-pre'`)
		_, _ = pool.Exec(ctx, `DELETE FROM hatamoto_commenters WHERE email_hash = ANY($1)`, hashes)
		_, _ = pool.Exec(ctx, `DELETE FROM hatamoto_import_runs WHERE source = $1`, SourceName+":https://"+host)
		_, _ = pool.Exec(ctx, `DELETE FROM hatamoto_import_map WHERE source = $1`, SourceName+":https://"+host)
	}
	cleanup()
	t.Cleanup(cleanup)

	// The site already has a comment row, as a site with live comments would.
	var preEntity string
	if err := pool.QueryRow(ctx, `
		INSERT INTO hatamoto_entities (schema_id, name, slug, status, attributes, published_at)
		SELECT id, 'Pre', 'wpc-pre', 'published', '{}', now() FROM hatamoto_entity_schemas WHERE slug = 'person' RETURNING id::text`).Scan(&preEntity); err != nil {
		t.Fatal(err)
	}
	preBody, err := commentfmt.Render("earlier")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := comment.NewImporter(db, commentsTestSecret).Import(ctx, []comment.ImportedComment{{
		Key: "pre", EntityID: uuid.MustParse(preEntity), AuthorName: "Pre", Email: "pre@example.test",
		BodyRaw: "earlier", BodyHTML: preBody.HTML, BodyHash: preBody.BodyHash, Status: comment.StatusApproved, CreatedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatal(err)
	}

	wxr := strings.ReplaceAll(commentsWXR, "old.example.test", host)
	dump, err := ParseWXR(strings.NewReader(wxr), WXROptions{})
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(pool, "https://"+host)
	run := func() *Runner {
		r, err := NewRunner(d, store, dump, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if r.run, err = store.StartRun(ctx, RunRunning, nil); err != nil {
			t.Fatal(err)
		}
		r.state = &SiteState{Mode: ModeExisting}
		for _, step := range []func(context.Context) error{r.prepareSchemas, r.importContentAndComments} {
			if err := step(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if r.res.Failed != 0 || len(r.res.Errors) > 0 {
			t.Fatalf("failed=%d errors=%+v", r.res.Failed, r.res.Errors)
		}
		r.res.Report = Scan(dump, store.Ref())
		if err := store.FinishRun(ctx, r.run.ID, RunCompleted, r.res, nil); err != nil {
			t.Fatal(err)
		}
		return r
	}
	r1 := run()
	cr := r1.res.Comments
	if cr == nil {
		t.Fatal("no comments result")
	}
	want := CommentsResult{
		Imported: 13, Approved: 9, Pending: 1, Spam: 1, Trash: 2, CommentersCreated: 10,
		PingsTotal: 2, SkippedNoEmail: 2, SkippedNoPost: 2, SkippedBody: 1, Reparented: 1, URLsDropped: 16,
	}
	got := *cr
	got.Pings = nil
	if !reflect.DeepEqual(got, want) {
		t.Errorf("comments result = %+v\nwant %+v", *cr, want)
	}
	if len(cr.Pings) != 2 || !strings.Contains(cr.Pings[0], "First Post (#") {
		t.Errorf("pings = %v", cr.Pings)
	}
	if cr.Enabled {
		t.Error("comments were turned on")
	}

	var n int
	count := func(q string, args ...any) int {
		t.Helper()
		if err := pool.QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for status, w := range map[string]int{"approved": 9, "pending": 1, "spam": 1, "trash": 2} {
		got := count(`SELECT count(*) FROM hatamoto_comments c JOIN hatamoto_entities e ON e.id = c.entity_id WHERE e.metadata->>'import_key' LIKE 'wp:`+host+`:%' AND c.status = $1`, status)
		if got != w {
			t.Errorf("%s comments = %d, want %d", status, got, w)
		}
	}

	// Threading.
	id := func(wp string) string {
		t.Helper()
		u, ok, err := store.Lookup(ctx, KindComment, wp)
		if err != nil || !ok {
			t.Fatalf("comment %s not mapped (%v)", wp, err)
		}
		return u.String()
	}
	var parent *string
	if err := pool.QueryRow(ctx, `SELECT parent_id::text FROM hatamoto_comments WHERE id = $1`, id("2")).Scan(&parent); err != nil || parent == nil || *parent != id("1") {
		t.Errorf("reply parent = %v (%v), want %s", parent, err, id("1"))
	}
	if err := pool.QueryRow(ctx, `SELECT parent_id::text FROM hatamoto_comments WHERE id = $1`, id("10")).Scan(&parent); err != nil || parent != nil {
		t.Errorf("orphan reply parent = %v (%v), want none", parent, err)
	}

	// No URL, IP or clear email anywhere: rows or the stored report.
	for _, q := range []string{
		`SELECT count(*) FROM hatamoto_comments c WHERE to_jsonb(c)::text LIKE '%url-sentinel%' OR to_jsonb(c)::text LIKE '%203.0.113.77%' OR to_jsonb(c)::text LIKE '%@example.test%'`,
		`SELECT count(*) FROM hatamoto_commenters c WHERE to_jsonb(c)::text LIKE '%url-sentinel%' OR to_jsonb(c)::text LIKE '%203.0.113.77%' OR to_jsonb(c)::text LIKE '%@example.test%'`,
		`SELECT count(*) FROM hatamoto_import_runs r WHERE r.report::text LIKE '%url-sentinel%' OR r.report::text LIKE '%203.0.113.77%' OR r.report::text LIKE '%@example.test%'`,
	} {
		if got := count(q); got != 0 {
			t.Errorf("%d rows leak personal data: %s", got, q)
		}
	}
	for _, w := range r1.res.Warnings {
		if strings.Contains(w, "@") || strings.Contains(w, "203.0.113") {
			t.Errorf("warning leaks personal data: %q", w)
		}
	}

	// Hashes match the live path; the sealed email opens.
	ipHash, netHash := keys.HashIP("203.0.113.77")
	var gotIP, gotNet string
	if err := pool.QueryRow(ctx, `SELECT ip_hash, net_hash FROM hatamoto_comments WHERE id = $1`, id("1")).Scan(&gotIP, &gotNet); err != nil || gotIP != ipHash || gotNet != netHash {
		t.Errorf("ip hashes = %q %q (%v), want %q %q", gotIP, gotNet, err, ipHash, netHash)
	}
	var sealed []byte
	annHash := keys.HashEmail("ann@example.test")
	if err := pool.QueryRow(ctx, `SELECT email_sealed FROM hatamoto_commenters WHERE email_hash = $1`, annHash).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if email, ok := keys.OpenEmail(sealed, annHash); !ok || email != "ann@example.test" {
		t.Errorf("OpenEmail = %q, %v", email, ok)
	}

	// The body is the renderer's output for the converted comment.
	wantBody, err := commentfmt.Render(commentHTMLToText("<p>Reply <strong>one</strong></p>"))
	if err != nil {
		t.Fatal(err)
	}
	var html, bodyHash string
	var hasLinks bool
	if err := pool.QueryRow(ctx, `SELECT body_html, body_hash, has_links FROM hatamoto_comments WHERE id = $1`, id("2")).Scan(&html, &bodyHash, &hasLinks); err != nil {
		t.Fatal(err)
	}
	if html != wantBody.HTML || bodyHash != wantBody.BodyHash || hasLinks != wantBody.HasLinks {
		t.Errorf("stored body = %q %q %v, want %q %q %v", html, bodyHash, hasLinks, wantBody.HTML, wantBody.BodyHash, wantBody.HasLinks)
	}

	// Trust: capped at known, never trusted or blocked.
	if got := count(`SELECT count(*) FROM hatamoto_commenters WHERE email_hash = ANY($1) AND trust_level IN ('trusted','blocked')`, hashes); got != 0 {
		t.Errorf("%d commenters are trusted or blocked", got)
	}
	var trust string
	if err := pool.QueryRow(ctx, `SELECT trust_level FROM hatamoto_commenters WHERE email_hash = $1`, keys.HashEmail("rita@example.test")).Scan(&trust); err != nil || trust != "known" {
		t.Errorf("rita trust = %q (%v), want known", trust, err)
	}

	// Comments stay off, with live comments already present.
	set, err := comment.NewSettingsStore(pool).Get(ctx)
	if err != nil || set.Enabled {
		t.Errorf("comments enabled = %v (%v), want off", set.Enabled, err)
	}

	// Counts live in the entity counts row.
	first := r1.infos["10"].ent.ID
	var cnt, pend int
	if err := pool.QueryRow(ctx, `SELECT comment_count, pending_count FROM hatamoto_comment_entities WHERE entity_id = $1`, first).Scan(&cnt, &pend); err != nil || cnt != 4 || pend != 1 {
		t.Errorf("counts = %d, %d (%v), want 4, 1", cnt, pend, err)
	}

	// A moderator's change survives a re-run, which adds nothing.
	if _, err := pool.Exec(ctx, `UPDATE hatamoto_comments SET status = 'approved' WHERE id = $1`, id("4")); err != nil {
		t.Fatal(err)
	}
	before := count(`SELECT count(*) FROM hatamoto_comments`)
	r2 := run()
	if r2.res.Comments == nil || r2.res.Comments.Imported != 0 || r2.res.Comments.AlreadyImported != cr.Imported {
		t.Errorf("re-run result = %+v, want 0 imported and %d already imported", r2.res.Comments, cr.Imported)
	}
	if after := count(`SELECT count(*) FROM hatamoto_comments`); after != before {
		t.Errorf("re-run changed the comment count %d -> %d", before, after)
	}
	var st string
	if err := pool.QueryRow(ctx, `SELECT status FROM hatamoto_comments WHERE id = $1`, id("4")).Scan(&st); err != nil || st != "approved" {
		t.Errorf("moderated comment status = %q (%v), want approved", st, err)
	}
}
