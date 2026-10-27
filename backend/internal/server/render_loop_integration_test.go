//go:build integration

package server

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
)

// B7 across HTTP with the real cache: a paged builder loop on a page, the
// published-only rules, cache purge on a write, and an archive built in the
// builder with slot.replace. Needs a migrated Postgres:
//
//	DATABASE_URL=... go test -tags integration ./internal/server -run RenderLoop
func TestRenderLoop_Integration(t *testing.T) {
	r := newReuseIT(t)
	if err := r.srv.renderEngine.SetTheme(themeFixture(t, "theme_b7.json")); err != nil {
		t.Fatal(err)
	}
	base := "/b7-" + r.sfx
	schema := "b7-" + r.sfx
	schemaID := uuid.New()
	r.exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,'B7 Posts',$2,'{"type":"object"}',$3)`,
		schemaID, schema, fmt.Sprintf(`{"url_pattern":"%s/{path}","archive_path":"%s"}`, base, base))
	t.Cleanup(func() {
		r.exec(`DELETE FROM hatamoto_entities WHERE schema_id=$1`, schemaID)
		r.exec(`DELETE FROM hatamoto_entity_schemas WHERE id=$1`, schemaID)
	})
	// 30 published posts (Post 01 newest), 2 drafts, a hidden and a visible person.
	for i := 1; i <= 30; i++ {
		r.exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes)
			VALUES ($1,$2,$3,$4,'published', now() - ($5 || ' hours')::interval, '{}'::jsonb)`,
			uuid.New(), schemaID, fmt.Sprintf("Post %02d", i), fmt.Sprintf("p%d", i), fmt.Sprint(i))
	}
	for i := 1; i <= 2; i++ {
		r.exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,attributes) VALUES ($1,$2,$3,$4,'draft','{}'::jsonb)`,
			uuid.New(), schemaID, fmt.Sprintf("Draft %02d", i), fmt.Sprintf("d%d", i))
	}
	for name, public := range map[string]string{"Visible Vera": "true", "Hidden Hank": "false"} {
		id := uuid.New()
		r.exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,published_at,attributes)
			SELECT $1, id, $2::text, $3::text, 'published', now(), jsonb_build_object('public', $4::boolean)
			FROM hatamoto_entity_schemas WHERE slug='person'`, id, name, "b7-"+strings.ToLower(strings.Fields(name)[1])+"-"+r.sfx, public)
		r.t.Cleanup(func() { r.exec(`DELETE FROM hatamoto_entities WHERE id=$1`, id) })
	}
	block := r.mk(r.blockSc, "B7 Secret Block", map[string]any{"page_doc": headingDoc("bk", "B7-SECRET-BLOCK")})
	r.exec(`UPDATE hatamoto_entities SET status='published', published_at=now() WHERE id=$1`, block.ID) // a stray published row must still never list

	loop := func(id string, p map[string]any, field string) map[string]any {
		return map[string]any{"id": id, "t": "loop", "p": p, "c": []any{
			map[string]any{"id": id + "h", "t": "heading", "b": map[string]string{"text": field}},
			map[string]any{"id": id + "e", "t": "loopempty", "c": []any{map[string]any{"id": id + "x", "t": "text", "p": map[string]any{"text": "B7-EMPTY"}}}},
		}}
	}
	doc := func(nodes ...any) map[string]any { return map[string]any{"v": 2, "nodes": nodes} }
	items := func(body string) int { return strings.Count(body, `class="hm-loop__item"`) }
	titles := func(body string) string {
		return strings.Join(regexp.MustCompile(`Post \d\d`).FindAllString(body, -1), ",")
	}

	// (a) a page: bound heading + a paged loop of the 12 newest posts.
	pagedDoc := doc(
		map[string]any{"id": "hd", "t": "heading", "p": map[string]any{"level": 1}, "b": map[string]string{"text": "entity.name"}},
		loop("lp", map[string]any{"schema": schema, "limit": 12, "order": "newest", "paging": "pages"}, "loop.item.title"),
	)
	page := r.mk(r.pageSc, "B7 Loop Page", map[string]any{"page_doc": pagedDoc})
	r.publish(page)
	path := "/" + page.Slug
	for _, tc := range []struct {
		q          string
		first, n   string
		count      int
		prev, next bool
	}{
		{"", "Post 01", "Post 12", 12, false, true},
		{"?page=2", "Post 13", "Post 24", 12, true, true},
		{"?page=3", "Post 25", "Post 30", 6, true, false},
	} {
		w := r.get(path + tc.q)
		body := w.Body.String()
		if w.Code != 200 {
			t.Fatalf("%s: %d", tc.q, w.Code)
		}
		got := strings.Split(titles(body), ",")
		if items(body) != tc.count || got[0] != tc.first || got[len(got)-1] != tc.n || len(got) != tc.count {
			t.Errorf("%q: items=%d titles=%v", tc.q, items(body), got)
		}
		canon := `<link rel="canonical" href="https://b6-it.example.test` + path + tc.q + `"`
		if !strings.Contains(body, canon) {
			t.Errorf("%q: canonical missing %s", tc.q, canon)
		}
		if tc.prev != strings.Contains(body, `rel="prev"`) || tc.next != strings.Contains(body, `rel="next"`) {
			t.Errorf("%q: rel prev/next mismatch", tc.q)
		}
		if tc.q != "" && !strings.Contains(body, `aria-current="page"`) {
			t.Errorf("%q: no aria-current", tc.q)
		}
		if !strings.Contains(body, ">B7 Loop Page</h1>") {
			t.Errorf("%q: bound entity.name missing", tc.q)
		}
		for _, bad := range []string{"Draft 0", "B7-SECRET-BLOCK", "Hidden Hank"} {
			if strings.Contains(body, bad) {
				t.Errorf("%q leaks %s", tc.q, bad)
			}
		}
	}
	if w := r.get(path + "?page=4"); w.Code != 404 {
		t.Errorf("?page=4: %d, want 404", w.Code)
	}

	// Cache: the second ?page=2 is a hit, a write purges it, and the new title shows.
	p2 := path + "?page=2"
	r.get(p2)
	r.eventually("?page=2 is cached", func() bool { return r.cached(p2) })
	hit := r.get(p2).Body.String()
	if !strings.Contains(hit, "Post 13") {
		t.Fatal("cached page 2 lost its items")
	}
	var post13 uuid.UUID
	if err := r.db.Pool().QueryRow(r.ctx, `SELECT id FROM hatamoto_entities WHERE schema_id=$1 AND slug='p13'`, schemaID).Scan(&post13); err != nil {
		t.Fatal(err)
	}
	name := "Renamed 13"
	if _, err := r.srv.entityService.UpdateEntity(r.ctx, post13, &r.owner.ID, &entity.UpdateEntityInput{Name: &name}); err != nil {
		t.Fatal(err)
	}
	r.eventually("an entity save purges ?page=2", func() bool { return !r.cached(p2) })
	if body := r.get(p2).Body.String(); !strings.Contains(body, "Renamed 13") || strings.Contains(body, ">Post 13<") {
		t.Error("the saved title did not reach page 2")
	}
	// A page without a paged loop is never cached under ?page=2.
	plain := r.mk(r.pageSc, "B7 Plain", map[string]any{"page_doc": headingDoc("h", "B7-PLAIN")})
	r.publish(plain)
	if w := r.get("/" + plain.Slug + "?page=2"); w.Code != 200 || r.cached("/"+plain.Slug+"?page=2") {
		t.Errorf("plain ?page=2: %d cached=%v", w.Code, r.cached("/"+plain.Slug+"?page=2"))
	}

	// (b) region-page shape: other loops. block rows never show, hidden people never show.
	blk := r.mk(r.pageSc, "B7 Block Loop", map[string]any{"page_doc": doc(loop("lb", map[string]any{"schema": "block"}, "loop.item.title"))})
	r.publish(blk)
	if body := r.get("/" + blk.Slug).Body.String(); items(body) != 0 || !strings.Contains(body, "B7-EMPTY") || strings.Contains(body, "B7-SECRET-BLOCK") {
		t.Errorf("a loop over block: items=%d", items(body))
	}
	ppl := r.mk(r.pageSc, "B7 People", map[string]any{"page_doc": doc(loop("lq", map[string]any{"schema": "person", "limit": 48}, "loop.item.title"))})
	r.publish(ppl)
	if body := r.get("/" + ppl.Slug).Body.String(); !strings.Contains(body, "Visible Vera") || strings.Contains(body, "Hidden Hank") {
		t.Error("people loop: visible/hidden rule broken")
	}
	// order, offset, limit and exclude_current.
	ord := r.mk(r.pageSc, "B7 Ordered", map[string]any{"page_doc": doc(loop("lo", map[string]any{"schema": schema, "limit": 3, "offset": 2, "order": "oldest"}, "loop.item.title"))})
	r.publish(ord)
	if got := titles(r.get("/" + ord.Slug).Body.String()); got != "Post 28,Post 27,Post 26" {
		t.Errorf("oldest offset 2 limit 3 = %s", got)
	}
	ex := r.mk(r.pageSc, "B7 Exclude Me", map[string]any{"page_doc": doc(loop("lx", map[string]any{"schema": "page", "limit": 48, "exclude_current": true}, "loop.item.name"))})
	r.publish(ex)
	exBody := r.get("/" + ex.Slug).Body.String()
	if !strings.Contains(exBody, `-lxh">B7 Loop Page</h2>`) || strings.Contains(exBody, `-lxh">B7 Exclude Me</h2>`) {
		t.Error("exclude_current: the other pages must list and the page itself must not")
	}

	// (c) the archive built in the builder: the part replaces the theme's listing.
	archive := base
	listing := r.get(archive)
	if listing.Code != 200 {
		t.Fatalf("archive: %d", listing.Code)
	}
	before := listing.Body.String()
	part := r.mk(r.partSc, "B7 Archive", map[string]any{"slot": "archive", "page_doc": doc(
		map[string]any{"id": "ah", "t": "heading", "p": map[string]any{"text": "B7-ARCHIVE-PART", "level": 1}},
		loop("la", map[string]any{"from": "archive", "paging": "pages"}, "loop.item.title"),
		map[string]any{"id": "sl", "t": "slot", "p": map[string]any{"replace": true}},
	)})
	if code := r.do("owner", "POST", "/admin/theme/parts", url.Values{"part_archive": {part.ID.String()}}).Code; code != 303 {
		t.Fatalf("assign archive part: %d", code)
	}
	t.Cleanup(func() { r.do("owner", "POST", "/admin/theme/parts", url.Values{}) })
	r.eventually("the archive is not the stale listing", func() bool { return strings.Contains(r.get(archive).Body.String(), "B7-ARCHIVE-PART") })
	after := r.get(archive).Body.String()
	if items(after) == 0 || !strings.Contains(after, "Post 01") || strings.Contains(after, "Draft 0") {
		t.Errorf("archive loop: items=%d", items(after))
	}
	if strings.Count(before, "Post 01") == 0 {
		t.Error("the theme's own listing never showed the posts (test premise)")
	}
	if !strings.Contains(after, `<nav class="hm-pager"`) || strings.Contains(after, `class="hm-card`) && strings.Contains(before, `class="hm-card`) {
		t.Errorf("archive pager / theme listing still present")
	}
	if w := r.get(archive + "?page=2"); w.Code != 200 || !strings.Contains(w.Body.String(), "B7-ARCHIVE-PART") {
		t.Errorf("archive page 2: %d", w.Code)
	}
	if w := r.get(archive + "?page=99"); w.Code != 404 {
		t.Errorf("archive page 99: %d", w.Code)
	}
}
