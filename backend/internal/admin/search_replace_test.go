package admin

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/menu"
)

// selTokens are the match tokens of a preview page, as a browser would post them.
var selTokens = regexp.MustCompile(`name="sel" value="([^"]+)"`)

func tokensIn(body string) []string {
	var out []string
	for _, m := range selTokens.FindAllStringSubmatch(body, -1) {
		out = append(out, strings.ReplaceAll(m[1], "&#43;", "+"))
	}
	return out
}

// fakeTracked says every listed entity belongs to a plugin.
type fakeTracked map[uuid.UUID]bool

func (f fakeTracked) Get(_ context.Context, id uuid.UUID) (*changeset.Ownership, error) {
	if f[id] {
		return &changeset.Ownership{State: changeset.OwnershipTracked, Plugin: "importer"}, nil
	}
	return &changeset.Ownership{}, nil
}
func (f fakeTracked) HandBack(context.Context, uuid.UUID) (string, error) { return "", nil }

const srPreviewPath = "/admin/settings/search-replace?find=first&replace=second&mode=plain&match_case=1&in=content&in=seo&in=other&in=menus"

func TestSearchReplaceFormGolden(t *testing.T) {
	c := newContentRig(t)
	body := normalizePage(c.get(t, "owner-token", "/admin/settings/search-replace", ""))
	compareGolden(t, "search-replace-form.html", body)
	has(t, body, `<legend class="hm-field__label">Look in</legend>`, `name="find"`, `method="get"`)
	if strings.Contains(body, `name="sel"`) {
		t.Error("the empty form lists matches")
	}
}

func TestSearchReplacePreviewGolden(t *testing.T) {
	c := newContentRig(t)
	body := normalizePage(c.get(t, "owner-token", srPreviewPath, ""))
	compareGolden(t, "search-replace-preview.html", body)
	has(t, body, "1 match in 1 item. Nothing has been changed yet.", "Alpha service", "Replace 1 selected match in 1 item", `method="post"`, `name="_csrf"`)
	if n := len(tokensIn(body)); n != 1 {
		t.Fatalf("tokens = %d", n)
	}
	// Nothing was written by looking.
	if got := string(c.ents.ents[fid(11)].Attributes); !strings.Contains(got, "The first") || c.ents.ents[fid(11)].Version != 1 {
		t.Errorf("preview changed the entity: %s", got)
	}
}

func TestSearchReplaceApplyWritesAuditsAndRestores(t *testing.T) {
	c := newContentRig(t)
	var events []AuditEvent
	c.a.deps.Audit = func(_ context.Context, e AuditEvent) { events = append(events, e) }
	body := c.get(t, "owner-token", srPreviewPath, "")
	rec := c.post("/admin/settings/search-replace", url.Values{
		"find": {"first"}, "replace": {"second"}, "mode": {"plain"}, "match_case": {"1"}, "in": {"content", "seo", "other", "menus"},
		"sel": tokensIn(body),
	})
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/search-replace" {
		t.Fatalf("apply: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	e := c.ents.ents[fid(11)]
	if !strings.Contains(string(e.Attributes), "The second") || e.Version != 2 {
		t.Errorf("entity: v%d %s", e.Version, e.Attributes)
	}
	vs := c.ents.versions[fid(11)]
	if len(vs) != 1 || vs[0].ChangeSummary == nil || *vs[0].ChangeSummary != `Search and replace: "first" -> "second"` || !strings.Contains(string(vs[0].Attributes), "The first") {
		t.Errorf("versions: %+v", vs)
	}
	has(t, c.next(rec, "/admin/settings/search-replace"), "Replaced 1 match in 1 item.", "Versions tab")
	if len(events) != 1 || events[0].Action != "search_replace.apply" {
		t.Fatalf("audit: %+v", events)
	}
	d := events[0].Detail
	if d["find"] != "first" || d["replace"] != "second" || d["matches"] != 1 || d["items"] != 1 || d["menu_items"] != 0 || d["skipped"] != 0 || d["failed"] != 0 || d["match_case"] != true || d["whole_word"] != false {
		t.Errorf("audit detail: %+v", d)
	}
}

func TestSearchReplaceSkipsItemEditedSincePreview(t *testing.T) {
	c := newContentRig(t)
	body := c.get(t, "owner-token", srPreviewPath, "")
	raw := json.RawMessage(`{"summary":"The first, edited meanwhile","featured":true}`)
	if _, err := c.ents.UpdateEntity(context.Background(), fid(11), nil, updateAttrs(&raw)); err != nil {
		t.Fatal(err)
	}
	rec := c.post("/admin/settings/search-replace", url.Values{
		"find": {"first"}, "replace": {"second"}, "mode": {"plain"}, "match_case": {"1"}, "in": {"other"}, "sel": tokensIn(body),
	})
	got := c.next(rec, "/admin/settings/search-replace")
	has(t, got, "Nothing was replaced.", "1 item skipped because it has changed since the preview.")
	if !strings.Contains(string(c.ents.ents[fid(11)].Attributes), "edited meanwhile") || strings.Contains(string(c.ents.ents[fid(11)].Attributes), "second") {
		t.Errorf("stale item was written: %s", c.ents.ents[fid(11)].Attributes)
	}
}

func TestSearchReplaceOwnerOnlyAndCSRF(t *testing.T) {
	c := newContentRig(t)
	if rec := c.do("GET", srPreviewPath, "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor GET = %d", rec.Code)
	}
	form := url.Values{"find": {"first"}, "in": {"other"}, "sel": {"junk"}}
	if rec := c.do("POST", "/admin/settings/search-replace", "editor-token", form, nil); rec.Code != 403 {
		t.Errorf("editor POST = %d", rec.Code)
	}
	if rec := c.do("GET", srPreviewPath, "", nil, nil); rec.Code != 303 {
		t.Errorf("anonymous GET = %d", rec.Code)
	}
	// Without the synchronizer token the write is refused.
	c.realTokens()
	rec := c.do("POST", "/admin/settings/search-replace", "owner-token", form, map[string]string{"X-CSRF-Token": ""})
	if rec.Code != 403 {
		t.Errorf("POST without token = %d", rec.Code)
	}
	if c.ents.ents[fid(11)].Version != 1 {
		t.Error("a refused post wrote")
	}
}

func TestSearchReplaceEmptyFindAndNoPlace(t *testing.T) {
	c := newContentRig(t)
	rec := c.do("GET", "/admin/settings/search-replace?find=&in=content", "owner-token", nil, nil)
	has(t, rec.Body.String(), "There is a problem with this form", "Enter the text or address to find.")
	if rec.Code != 422 {
		t.Errorf("empty find = %d", rec.Code)
	}
	rec = c.do("GET", "/admin/settings/search-replace?find=x", "owner-token", nil, nil)
	has(t, rec.Body.String(), "Choose at least one place to look.")
}

func TestSearchReplacePluginOwnedStartsUnticked(t *testing.T) {
	c := newContentRig(t)
	c.a.deps.Content.Ownership = fakeTracked{fid(11): true}
	body := c.get(t, "owner-token", srPreviewPath, "")
	has(t, body, "Replacing takes ownership from the plugin importer.", "Replace 0 selected matches in 0 items")
	if strings.Contains(body, `name="sel" value="`+tokensIn(body)[0]+`" checked`) {
		t.Error("plugin-owned match is ticked")
	}
}

func TestSearchReplaceMenus(t *testing.T) {
	c := newContentRig(t)
	fm := newFakeMenus()
	u := "https://old.example/shop"
	fm.add("Main", "header", &menu.MenuItem{ID: fid(61), Label: "Old shop", URL: &u})
	c.a.deps.Growth.Menus = fm
	body := c.get(t, "owner-token", "/admin/settings/search-replace?find=old.example&replace=new.example&mode=plain&in=menus", "")
	has(t, body, "Menu: Main", "Menus have no version history and cannot be restored.")
	rec := c.post("/admin/settings/search-replace", url.Values{"find": {"old.example"}, "replace": {"new.example"}, "mode": {"plain"}, "in": {"menus"}, "sel": tokensIn(body)})
	has(t, c.next(rec, "/admin/settings/search-replace"), "Replaced 1 match in 1 menu item.")
	if got := *fm.menus[firstMenu(fm)].Items[0].URL; got != "https://new.example/shop" {
		t.Errorf("menu url = %s", got)
	}
}

func firstMenu(f *fakeMenus) uuid.UUID {
	for id := range f.menus {
		return id
	}
	return uuid.Nil
}

func updateAttrs(a *json.RawMessage) *entity.UpdateEntityInput {
	return &entity.UpdateEntityInput{Attributes: a}
}
