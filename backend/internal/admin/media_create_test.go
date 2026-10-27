package admin

import (
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/media"
)

func TestMediaUploadPageAndCreatedRedirect(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	if page := c.get(t, "owner-token", "/admin/settings/media/new", ""); !strings.Contains(page, `type="file"`) {
		t.Fatalf("new page has no file input")
	}
	list := c.get(t, "owner-token", "/admin/settings/media", "")
	if !strings.Contains(list, `data-hm-open="upload"`) || !strings.Contains(list, `href="/admin/settings/media/new"`) {
		t.Errorf("header Upload link missing")
	}
	rec := c.upload(t, "/admin/media/upload", map[string][]byte{"new.png": png1x1}, true)
	loc := rec.Header().Get("Location")
	if rec.Code != 303 || !strings.HasPrefix(loc, "/admin/settings/media?created=") {
		t.Fatalf("upload: %d %q", rec.Code, loc)
	}
}

// A real upload lands on a page that shows the new file: the grid pins and marks the
// tile, and the list view the user came from is kept.
func TestMediaUploadHighlightsInGridAndKeepsView(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	rec := c.upload(t, "/admin/media/upload", map[string][]byte{"fresh.png": png1x1}, true)
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/admin/settings/media?created=") {
		t.Fatalf("grid upload location %q", loc)
	}
	id := strings.TrimPrefix(loc, "/admin/settings/media?created=")
	grid := c.get(t, "owner-token", loc, "")
	if !strings.Contains(grid, `class="hm-mediatile hm-mediatile--new" id="media-`+id+`"`) && !strings.Contains(grid, `hm-mediatile--new" id="media-`+id+`"`) {
		t.Errorf("grid tile for the new file is not marked new")
	}
	if first := strings.Index(grid, `<li><figure`); first >= 0 && !strings.HasPrefix(grid[first:], `<li><figure class="hm-mediatile hm-mediatile--new"`) {
		t.Errorf("new tile is not first in the grid")
	}
	if !strings.Contains(grid, "(new)") {
		t.Errorf("no hidden (new) text on the tile")
	}

	rec = c.upload(t, "/admin/media/upload?view=list", map[string][]byte{"second.png": png1x1}, true)
	loc = rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/admin/settings/media?view=list&created=") {
		t.Fatalf("list upload lost the view: %q", loc)
	}
	if list := c.get(t, "owner-token", loc, ""); !strings.Contains(list, `class="hm-row--new"`) {
		t.Errorf("list view row is not marked new")
	}
}

func TestStorageBarSplitsBytesByType(t *testing.T) {
	t.Parallel()

	items := []*media.Media{
		{MimeType: "image/png", FileSize: 3 << 20}, {MimeType: "image/webp", FileSize: 1 << 20},
		{MimeType: "application/pdf", FileSize: 2 << 20}, {MimeType: "video/mp4", FileSize: 4 << 20}, {MimeType: "text/plain", FileSize: 100},
	}
	got := map[string]float64{}
	for _, s := range storageBar(items).Segments {
		got[s.Label] = s.N
	}
	want := map[string]float64{"Images": 4 << 20, "Documents": 2 << 20, "Video": 4 << 20, "Other": 100}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
}

// Ticking tiles posts to /admin/media/bulk: without confirmation it answers a
// confirm page and deletes nothing; confirmed, it deletes.
func TestMediaBulkDeleteConfirmsThenDeletes(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	loc := c.upload(t, "/admin/media/upload", map[string][]byte{"a.png": png1x1}, true).Header().Get("Location")
	id := strings.TrimPrefix(loc, "/admin/settings/media?created=")
	grid := c.get(t, "owner-token", loc, "")
	for _, want := range []string{`action="/admin/media/bulk"`, `formaction="/admin/media/bulk"`, `data-hm-row-select`, `hm-media-storage`, `>New<`} {
		if !strings.Contains(grid, want) {
			t.Errorf("grid lacks %s", want)
		}
	}
	rec := c.do("POST", "/admin/media/bulk", "owner-token", url.Values{"action": {"delete"}, "id": {id}}, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Delete 1 file?") {
		t.Fatalf("unconfirmed bulk: %d", rec.Code)
	}
	if again := c.get(t, "owner-token", "/admin/media/"+id, ""); !strings.Contains(again, "a.png") {
		t.Fatalf("file deleted before confirmation")
	}
	rec = c.do("POST", "/admin/media/bulk", "owner-token", url.Values{"action": {"delete"}, "confirmed": {"1"}, "id": {id}}, nil)
	if rec.Code != 303 {
		t.Fatalf("confirmed bulk: %d", rec.Code)
	}
	if rec := c.do("GET", "/admin/media/"+id, "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("file still there: %d", rec.Code)
	}
}

// The detail page keeps the storage names under Technical details; the list
// search submits as you type and keeps the Filter button for no-JS.
func TestMediaDetailAndToolbarPolish(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	loc := c.upload(t, "/admin/media/upload", map[string][]byte{"p.png": png1x1}, true).Header().Get("Location")
	id := strings.TrimPrefix(loc, "/admin/settings/media?created=")
	detail := c.get(t, "owner-token", "/admin/media/"+id, "")
	if i := strings.Index(detail, "Technical details"); i < 0 || strings.Index(detail, "Stored as") < i {
		t.Errorf("Stored as is not inside Technical details")
	}
	list := c.get(t, "owner-token", "/admin/settings/media", "")
	for _, want := range []string{`hx-trigger="input changed delay:300ms from:#media-q, change"`, `>Filter<`} {
		if !strings.Contains(list, want) {
			t.Errorf("toolbar lacks %s", want)
		}
	}
}

// The storage scan reads up to 2,000 rows, so it runs on the unfiltered first full page load only:
// not on htmx search swaps, filters or later pages. The bar sits outside the swapped results.
func TestMediaStorageBarOnlyOnUnfilteredFullLoad(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	c.upload(t, "/admin/media/upload", map[string][]byte{"s.png": png1x1}, true)
	full := c.get(t, "owner-token", "/admin/settings/media", "")
	bar, results := strings.Index(full, `class="hm-media-storage"`), strings.Index(full, `id="media-results"`)
	if bar < 0 || results < 0 || bar > results {
		t.Fatalf("storage bar missing or inside #media-results (bar %d, results %d)", bar, results)
	}
	hx := c.do("GET", "/admin/settings/media?q=s", "owner-token", nil, map[string]string{"HX-Request": "true"})
	if strings.Contains(hx.Body.String(), "hm-media-storage") {
		t.Errorf("htmx search swap recomputed the storage bar")
	}
	if filtered := c.get(t, "owner-token", "/admin/settings/media?q=s", ""); strings.Contains(filtered, "hm-media-storage") {
		t.Errorf("a filtered load scanned the library")
	}
}

// Search swaps #media-results; the Grid/List tabs sit outside it, so they are swapped out of band
// to keep the query, or switching view would drop the search.
func TestMediaViewTabsKeepTheSearch(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	full := c.get(t, "owner-token", "/admin/settings/media", "")
	if !strings.Contains(full, `hx-select-oob="#media-tabs"`) || !strings.Contains(full, `id="media-tabs"`) {
		t.Fatalf("search form does not swap the view tabs")
	}
	hx := c.do("GET", "/admin/settings/media?q=logo", "owner-token", nil, map[string]string{"HX-Request": "true"}).Body.String()
	i := strings.Index(hx, `id="media-tabs"`)
	if i < 0 || !strings.Contains(hx[i:], "q=logo") {
		t.Errorf("view tabs in the swap do not carry the query")
	}
}

// Bulk delete goes through the server confirm page, which warns about files that are still used,
// and rejects empty, oversized, wrong-action and foreign-redirect requests without deleting.
func TestMediaBulkDeleteWarnsAndRejectsBadInput(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	grid := c.get(t, "owner-token", "/admin/settings/media", "")
	if strings.Contains(grid, "data-hm-bulk-confirm") {
		t.Error("bulk delete must use the server confirm page so the in-use warning shows")
	}
	used, free := fid(51).String(), fid(52).String()
	rec := c.do("POST", "/admin/media/bulk", "owner-token", url.Values{"action": {"delete"}, "id": {used, free}}, nil)
	if body := rec.Body.String(); rec.Code != 200 || !strings.Contains(body, "1 of these files is used") {
		t.Errorf("confirm page lacks the in-use warning: %d", rec.Code)
	}
	rec = c.do("POST", "/admin/media/bulk", "owner-token", url.Values{"action": {"delete"}, "id": {free}}, nil)
	if strings.Contains(rec.Body.String(), "used on your site") {
		t.Error("warned about a file that is not used")
	}
	many := make([]string, 101)
	for i := range many {
		many[i] = free
	}
	for name, form := range map[string]url.Values{
		"no ids":       {"action": {"delete"}, "confirmed": {"1"}},
		"over 100 ids": {"action": {"delete"}, "confirmed": {"1"}, "id": many},
		"other action": {"action": {"archive"}, "confirmed": {"1"}, "id": {free}},
	} {
		rec := c.do("POST", "/admin/media/bulk", "owner-token", form, nil)
		if rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), "/admin/settings/media") {
			t.Errorf("%s: %d %q", name, rec.Code, rec.Header().Get("Location"))
		}
		if got := c.do("GET", "/admin/media/"+free, "owner-token", nil, nil); got.Code != 200 {
			t.Fatalf("%s deleted a file", name)
		}
	}
	rec = c.do("POST", "/admin/media/bulk", "owner-token", url.Values{"action": {"delete"}, "confirmed": {"1"}, "id": {"not-a-uuid", free}, "back": {"//evil.example"}}, nil)
	if loc := rec.Header().Get("Location"); rec.Code != 303 || !strings.HasPrefix(loc, "/admin/settings/media") {
		t.Errorf("foreign back followed: %d %q", rec.Code, loc)
	}
	if got := c.do("GET", "/admin/media/"+free, "owner-token", nil, nil); got.Code != 404 {
		t.Errorf("valid id not deleted alongside a malformed one: %d", got.Code)
	}
}

// The bar and the swapped results share one wrapper, so the list stays the first content block
// after the header (UI-D10) and the page markup stays balanced.
func TestMediaListWrapsBarAndResults(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	body := c.get(t, "owner-token", "/admin/settings/media", "")
	if !strings.Contains(body, `<div id="media-main">`) {
		t.Error("bar and results are not wrapped")
	}
	if o, c := strings.Count(body, "<div"), strings.Count(body, "</div>"); o != c {
		t.Errorf("unbalanced divs: %d open, %d closed", o, c)
	}
}
