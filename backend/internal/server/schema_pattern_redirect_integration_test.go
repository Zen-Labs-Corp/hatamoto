//go:build integration

package server

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
)

// A schema whose URL pattern changes keeps a 301 from every old path, leaves
// the redirects that already exist alone, and writes nothing when the pattern
// stays the same.
func TestSchemaPatternChangeRedirects_Integration(t *testing.T) {
	r := newNestedRig(t)
	r.svc.SetSchemaPatternObserver(r.srv.onSchemaPatternChanged)
	mk := func(slug string, publish bool) *entity.Entity {
		t.Helper()
		e, err := r.svc.CreateEntity(r.ctx, nil, &entity.CreateEntityInput{SchemaID: r.noteSch, Name: slug, Slug: slug, Attributes: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		if publish {
			if _, err := r.svc.UpdateEntityStatus(r.ctx, e.ID, nil, entity.StatusPublished); err != nil {
				t.Fatal(err)
			}
		}
		return e
	}
	mk("one", true)
	mk("two", true)
	mk("draft", false)
	// An owner's rule already sits at one of the old paths.
	r.exec(`INSERT INTO hatamoto_redirects (source_path, target_url, status_code, source_tag) VALUES ('/notes/two', '/archive', 301, 'review-it')`)
	r.srv.redirectService.InvalidateCache()

	settings := func(pattern string) *json.RawMessage {
		raw := json.RawMessage(`{"url_pattern":"` + pattern + `","archive_path":"/notes"}`)
		return &raw
	}
	// The same pattern again: nothing is written.
	if _, err := r.svc.UpdateSchema(r.ctx, r.noteSch, &entity.UpdateSchemaInput{Settings: settings("/notes/{slug}")}); err != nil {
		t.Fatal(err)
	}
	if got := r.redirectTarget("/notes/one"); got != "" {
		t.Errorf("an unchanged pattern wrote a redirect: /notes/one -> %q", got)
	}

	if _, err := r.svc.UpdateSchema(r.ctx, r.noteSch, &entity.UpdateSchemaInput{Settings: settings("/journal/{slug}")}); err != nil {
		t.Fatal(err)
	}
	if got := r.redirectTarget("/notes/one"); got != "/journal/one" {
		t.Errorf("/notes/one -> %q, want /journal/one", got)
	}
	if got := r.redirectTarget("/notes/draft"); got != "" {
		t.Errorf("a draft has no public URL to redirect: %q", got)
	}
	// The owner's rule survives, untouched.
	if got := r.redirectTarget("/notes/two"); got != "/archive" {
		t.Errorf("an existing redirect was changed: /notes/two -> %q", got)
	}
	var n int
	if err := r.db.Pool().QueryRow(r.ctx, `SELECT count(*) FROM hatamoto_redirects WHERE source_path = '/notes/two'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("rows at /notes/two = %d, %v", n, err)
	}

	// Changing it back makes /notes/one live again: the automatic redirect that
	// left it is retired and the new path redirects the other way, no loop.
	if _, err := r.svc.UpdateSchema(r.ctx, r.noteSch, &entity.UpdateSchemaInput{Settings: settings("/notes/{slug}")}); err != nil {
		t.Fatal(err)
	}
	if got := r.redirectTarget("/notes/one"); got != "" {
		t.Errorf("a live page still redirects away: /notes/one -> %q", got)
	}
	if got := r.redirectTarget("/journal/one"); got != "/notes/one" {
		t.Errorf("/journal/one -> %q, want /notes/one", got)
	}
}

// The built-in person type moving from /author/{slug} to /team/{slug} through
// the same UpdateSchema the Admin form calls (ISC-33, 34): /author/x answers
// 301 to /team/x, a person without a public page gets no redirect, an existing
// unrelated redirect row is untouched.
func TestPersonPatternChangeRedirects_Integration(t *testing.T) {
	r := newNestedRig(t)
	r.svc.SetSchemaPatternObserver(r.srv.onSchemaPatternChanged)
	before, err := r.svc.GetSchemaBySlug(r.ctx, "person")
	if err != nil {
		t.Fatal(err)
	}
	mk := func(public bool) string {
		slug := "pp-" + uuid.NewString()[:8]
		p, err := r.svc.CreateEntity(r.ctx, nil, &entity.CreateEntityInput{SchemaID: before.ID, Name: slug, Slug: slug, Attributes: json.RawMessage(fmt.Sprintf(`{"public":%t}`, public))})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = r.svc.DeleteEntity(r.ctx, p.ID) })
		// The status follows public: a public person is published, a hidden one a draft.
		return slug
	}
	slug, hidden := mk(true), mk(false)
	r.exec(`INSERT INTO hatamoto_redirects (source_path, target_url, status_code, source_tag) VALUES ('/author/chintan', '/blog', 301, 'review-it')`)
	t.Cleanup(func() {
		_, _ = r.db.Pool().Exec(r.ctx, `DELETE FROM hatamoto_redirects WHERE source_path = ANY($1)`, []string{"/author/" + slug, "/author/" + hidden, "/author/chintan"})
		raw := json.RawMessage(`{"url_pattern":"/author/{slug}"}`)
		_, _ = r.svc.UpdateSchema(r.ctx, before.ID, &entity.UpdateSchemaInput{Settings: &raw})
		_, _ = r.db.Pool().Exec(r.ctx, `DELETE FROM hatamoto_redirects WHERE source_path = ANY($1)`, []string{"/team/" + slug})
	})
	r.srv.redirectService.InvalidateCache()

	settings := json.RawMessage(`{"url_pattern":"/team/{slug}"}`)
	if _, err := r.svc.UpdateSchema(r.ctx, before.ID, &entity.UpdateSchemaInput{Settings: &settings}); err != nil {
		t.Fatal(err)
	}

	if got := r.redirectTarget("/author/" + slug); got != "/team/"+slug {
		t.Errorf("/author/%s -> %q, want /team/%s", slug, got, slug)
	}
	if got := r.redirectTarget("/author/" + hidden); got != "" {
		t.Errorf("a person without a page got a redirect: %q", got)
	}
	if got := r.redirectTarget("/author/chintan"); got != "/blog" {
		t.Errorf("an existing redirect did not survive: /author/chintan -> %q", got)
	}
	var n int
	if err := r.db.Pool().QueryRow(r.ctx, `SELECT count(*) FROM hatamoto_redirects WHERE source_path = '/author/chintan' AND target_url = '/blog'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("rows at /author/chintan = %d, %v", n, err)
	}
}
