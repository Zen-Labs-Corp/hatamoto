package admin

import (
	"context"
	"errors"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/menu"
)

type fakeMenus struct {
	mu       sync.Mutex
	menus    map[uuid.UUID]*menu.Menu
	replaced []replaceCall
	deleted  []uuid.UUID
	// replaceErr, when set, makes ReplaceMenu fail.
	replaceErr error
}

type replaceCall struct {
	ID     uuid.UUID
	Fields *menu.UpdateMenuInput
	Items  []menu.ReplaceItem
}

func newFakeMenus() *fakeMenus { return &fakeMenus{menus: map[uuid.UUID]*menu.Menu{}} }

func (f *fakeMenus) add(name string, loc string, items ...*menu.MenuItem) *menu.Menu {
	m := &menu.Menu{ID: uuid.New(), Name: name, Slug: strings.ToLower(name), Items: items, CreatedAt: time.Now()}
	if loc != "" {
		m.Location = &loc
	}
	f.menus[m.ID] = m
	return m
}

func (f *fakeMenus) ListMenus(context.Context) ([]*menu.Menu, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*menu.Menu
	for _, m := range f.menus {
		out = append(out, m)
	}
	return out, nil
}
func (f *fakeMenus) GetMenuWithItems(_ context.Context, id uuid.UUID) (*menu.Menu, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m, ok := f.menus[id]; ok {
		return m, nil
	}
	return nil, menu.ErrMenuNotFound
}
func (f *fakeMenus) CreateMenu(_ context.Context, in *menu.CreateMenuInput) (*menu.Menu, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.menus {
		if strings.EqualFold(m.Name, in.Name) {
			return nil, menu.ErrSlugAlreadyExists
		}
	}
	return f.add(in.Name, ptrOr(in.Location)), nil
}
func ptrOr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
func (f *fakeMenus) ReplaceMenu(_ context.Context, id uuid.UUID, fields *menu.UpdateMenuInput, items []menu.ReplaceItem) (*menu.Menu, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.replaceErr != nil {
		return nil, f.replaceErr
	}
	m, ok := f.menus[id]
	if !ok {
		return nil, menu.ErrMenuNotFound
	}
	f.replaced = append(f.replaced, replaceCall{id, fields, items})
	return m, nil
}
func (f *fakeMenus) DeleteMenu(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.menus[id]; !ok {
		return menu.ErrMenuNotFound
	}
	delete(f.menus, id)
	f.deleted = append(f.deleted, id)
	return nil
}

type fakePickers struct{ entities, terms []PickOption }

func (f fakePickers) SearchEntities(_ context.Context, q string, _ int) ([]PickOption, error) {
	return filterPicks(f.entities, q), nil
}
func (f fakePickers) SearchTerms(_ context.Context, q string, _ int) ([]PickOption, error) {
	return filterPicks(f.terms, q), nil
}
func (f fakePickers) EntityName(_ context.Context, id uuid.UUID) string {
	for _, o := range f.entities {
		if o.Value == id.String() {
			return o.Label
		}
	}
	return ""
}
func filterPicks(in []PickOption, q string) []PickOption {
	var out []PickOption
	for _, o := range in {
		if q == "" || strings.Contains(strings.ToLower(o.Label+" "+o.Hint), strings.ToLower(q)) {
			out = append(out, o)
		}
	}
	return out
}

func newMenuRig(t *testing.T) (*rig, *fakeMenus) {
	t.Helper()
	fm := newFakeMenus()
	g := newRig(t, func(d *Deps) {
		d.Growth.Menus = fm
		d.Growth.Pickers = fakePickers{
			entities: []PickOption{{Value: uuid.NewString(), Label: "About us", Hint: "page · about"}, {Value: uuid.NewString(), Label: "Contact", Hint: "page · contact"}},
			terms:    []PickOption{{Value: "/category/news", Label: "News", Hint: "category"}},
		}
	})
	return g, fm
}

func TestMenuListAndCreate(t *testing.T) {
	t.Parallel()

	g, fm := newMenuRig(t)
	fm.add("Main", "header")
	rec := g.do("GET", "/admin/theme/menus", "owner-token", nil, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Main") || !strings.Contains(rec.Body.String(), "Header") {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	if issues := a11y.Check(rec.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	rec = g.do("POST", "/admin/theme/menus", "owner-token", url.Values{"name": {"Footer links"}, "location": {"footer"}}, nil)
	if rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), "/admin/theme/menus?created=") {
		t.Fatalf("create: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	rec = g.do("POST", "/admin/theme/menus", "owner-token", url.Values{"name": {"main"}}, nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), `value="main"`) || !strings.Contains(rec.Body.String(), "already exists") {
		t.Errorf("duplicate name should re-render the form with the input kept: %d", rec.Code)
	}
}

func TestMenuNewPageAndErrors(t *testing.T) {
	t.Parallel()

	g, _ := newMenuRig(t)
	rec := g.do("GET", "/admin/theme/menus/new", "owner-token", nil, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `name="location"`) {
		t.Fatalf("new: %d", rec.Code)
	}
	rec = g.do("POST", "/admin/theme/menus", "owner-token", url.Values{"name": {"  "}, "location": {"nowhere"}}, nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "Give the menu a name") || !strings.Contains(rec.Body.String(), "listed locations") {
		t.Errorf("errors: %d", rec.Code)
	}
}

func TestMenuListPinsCreated(t *testing.T) {
	t.Parallel()

	g, fm := newMenuRig(t)
	fm.add("Aaa", "header")
	z := fm.add("Zzz", "footer")
	rec := g.do("GET", "/admin/theme/menus?created="+z.ID.String(), "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "hm-row--new") || strings.Index(body, "Zzz") > strings.Index(body, "Aaa") {
		t.Errorf("created row not pinned first: %d %.600s", rec.Code, body)
	}
}

// itemForm builds the form of items: each "label|type|depth|target" is one row.
func itemForm(name string, rows ...[4]string) url.Values {
	f := url.Values{"name": {name}, "location": {"header"}, "_op": {"save"}}
	for i, r := range rows {
		p := "i." + string(rune('0'+i)) + "."
		f.Set(p+"label", r[0])
		f.Set(p+"type", r[1])
		f.Set(p+"depth", r[2])
		switch r[1] {
		case "entity":
			f.Set(p+"entity", r[3])
		case "archive":
			f.Set(p+"term", r[3])
			f.Set(p+"term_prev", r[3])
		default:
			f.Set(p+"url", r[3])
		}
	}
	return f
}

func TestMenuSaveIsOneReplaceWithTheTree(t *testing.T) {
	t.Parallel()

	g, fm := newMenuRig(t)
	m := fm.add("Main", "header")
	ent := uuid.NewString()
	f := itemForm("Main menu", [4]string{"Home", "custom", "0", "/"}, [4]string{"About", "entity", "1", ent},
		[4]string{"Team", "custom", "2", "https://example.com/team"}, [4]string{"News", "archive", "0", "/category/news"})
	f.Set("i.1.entity_prev", "About us")
	rec := g.do("POST", "/admin/theme/menus/"+m.ID.String(), "owner-token", f, nil)
	if rec.Code != 303 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	if len(fm.replaced) != 1 {
		t.Fatalf("one transactional replace expected, got %d", len(fm.replaced))
	}
	c := fm.replaced[0]
	if *c.Fields.Name != "Main menu" || *c.Fields.Location != "header" || len(c.Items) != 4 {
		t.Fatalf("call: %+v", c)
	}
	wantParent := []int{-1, 0, 1, -1}
	wantType := []string{menu.LinkTypeCustom, menu.LinkTypeEntity, menu.LinkTypeExternal, menu.LinkTypeArchive}
	for i, it := range c.Items {
		if it.Parent != wantParent[i] || it.LinkType != wantType[i] {
			t.Errorf("item %d: parent %d type %s, want %d %s", i, it.Parent, it.LinkType, wantParent[i], wantType[i])
		}
	}
	if c.Items[1].EntityID == nil || c.Items[1].EntityID.String() != ent {
		t.Errorf("entity id not kept: %+v", c.Items[1])
	}
	if c.Items[3].URL == nil || *c.Items[3].URL != "/category/news" {
		t.Errorf("term archive path not kept: %+v", c.Items[3])
	}
}

func TestMenuSaveRejectsBadItemsAndSavesNothing(t *testing.T) {
	t.Parallel()

	g, fm := newMenuRig(t)
	m := fm.add("Main", "")
	f := itemForm("Main", [4]string{"", "custom", "0", "/"}, [4]string{"Bad", "custom", "0", "javascript:alert(1)"}, [4]string{"Typed", "entity", "0", ""})
	f.Set("i.2.entity_q", "no such page") // text that matches nothing is not a missing link: it is a mistake
	f.Set("i.1.class", `a"bad`)
	rec := g.do("POST", "/admin/theme/menus/"+m.ID.String(), "owner-token", f, nil)
	if rec.Code != 422 || len(fm.replaced) != 0 {
		t.Fatalf("want 422 and no save: %d replaced=%d", rec.Code, len(fm.replaced))
	}
	body := rec.Body.String()
	for _, want := range []string{"Enter a label", "Enter a path like /about", "Choose the page or post", "Classes cannot contain", `href="#i-0-label"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
}

func TestMenuNoJSEntityByTypedName(t *testing.T) {
	t.Parallel()

	g, fm := newMenuRig(t)
	m := fm.add("Main", "")
	f := itemForm("Main", [4]string{"Contact", "entity", "0", ""})
	f.Set("i.0.entity_q", "contact")
	if rec := g.do("POST", "/admin/theme/menus/"+m.ID.String(), "owner-token", f, nil); rec.Code != 303 {
		t.Fatalf("typed name should resolve: %d %s", rec.Code, rec.Body.String())
	}
	if len(fm.replaced) != 1 || fm.replaced[0].Items[0].EntityID == nil {
		t.Fatalf("entity not resolved: %+v", fm.replaced)
	}
}

func TestMenuStructuralOpsWorkWithoutJS(t *testing.T) {
	t.Parallel()

	g, fm := newMenuRig(t)
	m := fm.add("Main", "")
	base := func() url.Values {
		return itemForm("Main", [4]string{"A", "custom", "0", "/a"}, [4]string{"B", "custom", "0", "/b"}, [4]string{"B1", "custom", "1", "/b1"})
	}
	order := func(body string) string {
		type pos struct {
			l string
			i int
		}
		var ps []pos
		for _, l := range []string{"A", "B", "B1"} {
			if i := strings.Index(body, `value="`+l+`"`); i >= 0 {
				ps = append(ps, pos{l, i})
			}
		}
		for a := range ps {
			for b := a + 1; b < len(ps); b++ {
				if ps[b].i < ps[a].i {
					ps[a], ps[b] = ps[b], ps[a]
				}
			}
		}
		var s []string
		for _, p := range ps {
			s = append(s, p.l)
		}
		return strings.Join(s, ",")
	}
	// Moving B up swaps it, with its sub-item, above A.
	f := base()
	f.Set("_op", "up.1")
	rec := g.do("POST", "/admin/theme/menus/"+m.ID.String(), "owner-token", f, nil)
	if rec.Code != 200 || order(rec.Body.String()) != "B,B1,A" || len(fm.replaced) != 0 {
		t.Fatalf("up: %d order=%s saved=%d", rec.Code, order(rec.Body.String()), len(fm.replaced))
	}
	if !strings.Contains(rec.Body.String(), "not saved yet") {
		t.Error("an unsaved edit must say so")
	}
	if issues := a11y.Check(rec.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	// Remove takes the whole group; indent under the item above; outdent.
	f = base()
	f.Set("_op", "remove.1")
	body := g.do("POST", "/admin/theme/menus/"+m.ID.String(), "owner-token", f, nil).Body.String()
	if order(body) != "A" {
		t.Errorf("remove: %s", order(body))
	}
	f = base()
	f.Set("_op", "indent.1")
	body = g.do("POST", "/admin/theme/menus/"+m.ID.String(), "owner-token", f, nil).Body.String()
	if !strings.Contains(body, `name="i.1.depth" value="1"`) || !strings.Contains(body, `name="i.2.depth" value="2"`) {
		t.Error("indent should move the item and its sub-item one level in")
	}
	f = base()
	f.Set("_op", "outdent.2")
	body = g.do("POST", "/admin/theme/menus/"+m.ID.String(), "owner-token", f, nil).Body.String()
	if !strings.Contains(body, `name="i.2.depth" value="0"`) {
		t.Error("outdent should make B1 top level")
	}
	f = base()
	f.Set("_op", "indent.0")
	if body := g.do("POST", "/admin/theme/menus/"+m.ID.String(), "owner-token", f, nil).Body.String(); !strings.Contains(body, "cannot be indented") {
		t.Error("the first item cannot be indented")
	}
	f = base()
	f.Set("_op", "add")
	f.Set("add_type", "archive")
	body = g.do("POST", "/admin/theme/menus/"+m.ID.String(), "owner-token", f, nil).Body.String()
	if !strings.Contains(body, `name="i.3.label"`) {
		t.Fatal("add should append a fourth row")
	}
	// The new row's required Label must not hide in a closed <details>, which no
	// browser opens on a failed validation; labelled rows stay collapsed.
	d := strings.LastIndex(body[:strings.Index(body, `name="i.3.label"`)], "<details")
	if !strings.HasPrefix(body[d:], `<details class="hm-menutree__edit" open>`) {
		t.Errorf("the appended empty item should render its details open: %.60s", body[d:])
	}
	if strings.Count(body, `<details class="hm-menutree__edit">`) != 3 {
		t.Error("labelled items should stay collapsed")
	}
}

// rowsOf lists "label:depth" of rows, for comparing a tree at a glance.
func rowsOf(rows []menuRow) string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Label+":"+strconv.Itoa(r.Depth))
	}
	return strings.Join(out, " ")
}

func treeRows(spec ...string) []menuRow {
	var rows []menuRow
	for _, sp := range spec {
		l, d, _ := strings.Cut(sp, ":")
		n, _ := strconv.Atoi(d)
		rows = append(rows, menuRow{Label: l, Depth: n})
	}
	return rows
}

func TestMenuTreeOps(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		op, target string
		in         []string
		want       string
	}{
		"outdent keeps the following siblings under the parent": {"outdent.2", "", []string{"A:0", "B:0", "B1:1", "B2:1", "C:0"}, "A:0 B:0 B2:1 B1:0 C:0"},
		"outdent takes the sub-items along":                     {"outdent.1", "", []string{"A:0", "A1:1", "A11:2", "A2:1", "B:0"}, "A:0 A2:1 A1:0 A11:1 B:0"},
		"remove takes the sub-items":                            {"remove.1", "", []string{"A:0", "B:0", "B1:1", "B11:2", "C:0"}, "A:0 C:0"},
		"duplicate inserts a copy after the group":              {"dup.1", "", []string{"A:0", "B:0", "B1:1", "C:0"}, "A:0 B:0 B1:1 B:0 B1:1 C:0"},
		"move under a later top-level item":                     {"moveto.0", "3", []string{"A:0", "A1:1", "B:0", "C:0", "C1:1"}, "B:0 C:0 C1:1 A:1 A1:2"},
		"move under an earlier top-level item":                  {"moveto.3", "0", []string{"A:0", "A1:1", "B:0", "C:0"}, "A:0 A1:1 C:1 B:0"},
		"move to the top level":                                 {"moveto.1", "top", []string{"A:0", "A1:1", "A11:2", "B:0"}, "A:0 B:0 A1:0 A11:1"},
		"cannot move under itself":                              {"moveto.0", "0", []string{"A:0", "A1:1"}, "A:0 A1:1"},
		"cannot move under a nested item":                       {"moveto.0", "2", []string{"A:0", "B:0", "B1:1"}, "A:0 B:0 B1:1"},
	}
	for name, tc := range tests {
		got, _ := applyMenuOp(treeRows(tc.in...), tc.op, "", tc.target)
		if g := rowsOf(got); g != tc.want {
			t.Errorf("%s: got %q, want %q", name, g, tc.want)
		}
	}
	deep := treeRows("A:0", "B:0", "B1:1", "B2:2", "B3:3", "B4:4", "B5:5")
	if got, msg := applyMenuOp(deep, "moveto.1", "", "0"); rowsOf(got) != rowsOf(deep) || !strings.Contains(msg, "too many levels") {
		t.Errorf("a move past the depth limit must be refused: %s %q", rowsOf(got), msg)
	}
}

func TestMenuDepthIsClamped(t *testing.T) {
	t.Parallel()

	rows := []menuRow{{Depth: 3}, {Depth: 5}, {Depth: 1}, {Depth: -2}}
	normaliseDepths(rows)
	got := []int{rows[0].Depth, rows[1].Depth, rows[2].Depth, rows[3].Depth}
	want := []int{0, 1, 1, 0}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("depths = %v, want %v", got, want)
		}
	}
}

func TestSafeMenuURL(t *testing.T) {
	t.Parallel()

	for u, ok := range map[string]bool{
		"/about": true, "#top": true, "https://example.com/x": true, "mailto:a@b.co": true, "tel:+123": true,
		"javascript:alert(1)": false, "data:text/html,x": false, "//evil.example": false, "": false, "/a b": false, "https://": false, "ftp://x": false,
	} {
		if safeMenuURL(u) != ok {
			t.Errorf("safeMenuURL(%q) = %v, want %v", u, !ok, ok)
		}
	}
}

func TestMenuEditPageA11yAndDelete(t *testing.T) {
	t.Parallel()

	g, fm := newMenuRig(t)
	name := "About us"
	eid := uuid.New()
	m := fm.add("Main", "footer", &menu.MenuItem{Label: "About", LinkType: menu.LinkTypeEntity, EntityID: &eid, EntityName: &name,
		Children: []*menu.MenuItem{{Label: "Team", LinkType: menu.LinkTypeCustom, URL: ptr("/team")}}})
	rec := g.do("GET", "/admin/theme/menus/"+m.ID.String(), "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, `data-depth="1"`) || !strings.Contains(body, `value="About us"`) {
		t.Fatalf("edit: %d", rec.Code)
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	if open, shut := strings.Count(body, "<section"), strings.Count(body, "</section>"); open != shut {
		t.Errorf("unbalanced cards: %d <section> vs %d </section>", open, shut)
	}
	if n := strings.Count(body, `<span>Save</span>`); n != 1 {
		t.Errorf("want one visible Save button, got %d", n)
	}
	for _, want := range []string{`hm-menutree__target-summary">Page or post · About us<`, `hm-menutree__target-summary">Link · /team<`, `value="outdent.0" formnovalidate disabled`, `value="indent.0" formnovalidate data-hm-op="indent" disabled`, `data-hm-undo-all`, `data-hm-notice`, `data-hm-tpl="notice"`, `data-hm-warn`, `data-hm-savestate`, `data-hm-issues`, `id="menu-inspector"`, `id="menu-preview"`, `aria-label="Actions for About"`, `aria-label="Actions for Team"`} {
		if !strings.Contains(body, want) {
			t.Errorf("edit page lacks %s", want)
		}
	}
	for _, gone := range []string{"data-hm-undo-btn", "data-hm-undo-text", `data-hm-tpl="undo"`} {
		if strings.Contains(body, gone) {
			t.Errorf("edit page still carries the dead undo markup %s", gone)
		}
	}
	if rec := g.do("GET", "/admin/theme/menus/"+m.ID.String()+"/delete", "owner-token", nil, nil); rec.Code != 200 || len(fm.deleted) != 0 {
		t.Fatalf("delete confirmation page must not delete: %d", rec.Code)
	}
	if rec := g.do("POST", "/admin/theme/menus/"+m.ID.String()+"/delete", "owner-token", url.Values{}, nil); rec.Code != 303 || len(fm.deleted) != 1 {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec := g.do("GET", "/admin/theme/menus/"+m.ID.String(), "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("deleted menu: %d", rec.Code)
	}
}

func TestPickersReturnJSON(t *testing.T) {
	t.Parallel()

	g, _ := newMenuRig(t)
	rec := g.do("GET", "/admin/pick/entities?q=about", "owner-token", nil, nil)
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Type"), "json") || !strings.Contains(rec.Body.String(), "About us") || strings.Contains(rec.Body.String(), "Contact") {
		t.Fatalf("entities: %d %s", rec.Code, rec.Body.String())
	}
	if rec := g.do("GET", "/admin/pick/terms?q=", "owner-token", nil, nil); !strings.Contains(rec.Body.String(), "/category/news") {
		t.Errorf("terms: %s", rec.Body.String())
	}
	if rec := g.do("GET", "/admin/pick/entities?q=x", "", nil, nil); rec.Code == 200 {
		t.Error("pickers need a session")
	}
}

// Limits match the columns (name/label varchar(100), url varchar(500)): the
// editor says so before the database would, and the tree stays in the form.
func TestMenuLengthLimitsMatchTheColumns(t *testing.T) {
	t.Parallel()

	g, fm := newMenuRig(t)
	m := fm.add("Main", "")
	long := func(n int) string { return strings.Repeat("a", n) }
	for name, f := range map[string]url.Values{
		"name":  itemForm(long(101), [4]string{"Home", "custom", "0", "/"}),
		"label": itemForm("Main", [4]string{long(101), "custom", "0", "/"}),
		"url":   itemForm("Main", [4]string{"Home", "custom", "0", "/" + long(500)}),
	} {
		rec := g.do("POST", "/admin/theme/menus/"+m.ID.String(), "owner-token", f, nil)
		if rec.Code != 422 || len(fm.replaced) != 0 {
			t.Errorf("%s: want 422 and no save, got %d (replaced %d)", name, rec.Code, len(fm.replaced))
		}
	}
	ok := itemForm(long(100), [4]string{long(100), "custom", "0", "/" + long(499)})
	if rec := g.do("POST", "/admin/theme/menus/"+m.ID.String(), "owner-token", ok, nil); rec.Code != 303 {
		t.Errorf("exactly at the limits must save: %d", rec.Code)
	}
	create := g.do("POST", "/admin/theme/menus", "owner-token", url.Values{"name": {long(101)}, "location": {""}}, nil)
	if create.Code != 422 || len(fm.menus) != 1 {
		t.Errorf("too long a menu name must not be created: %d", create.Code)
	}
}

func TestMenuSaveFailureKeepsTheTree(t *testing.T) {
	t.Parallel()

	g, fm := newMenuRig(t)
	m := fm.add("Main", "")
	fm.replaceErr = errors.New("value too long for type character varying(100)")
	f := itemForm("Main", [4]string{"Home", "custom", "0", "/"}, [4]string{"Team", "custom", "1", "/team"})
	rec := g.do("POST", "/admin/theme/menus/"+m.ID.String(), "owner-token", f, nil)
	body := rec.Body.String()
	if rec.Code != 500 || !strings.Contains(body, "Your changes are still here") || !strings.Contains(body, `value="Team"`) || !strings.Contains(body, `value="/team"`) {
		t.Fatalf("a failed save must re-render the form with the tree: %d", rec.Code)
	}
}

func TestMenuNoJSShowsOnlyTheTargetOfItsType(t *testing.T) {
	t.Parallel()

	g, fm := newMenuRig(t)
	m := fm.add("Main", "")
	f := itemForm("Main", [4]string{"Home", "custom", "0", "/"})
	f.Set("_op", "refresh")
	body := g.do("POST", "/admin/theme/menus/"+m.ID.String(), "owner-token", f, nil).Body.String()
	if !strings.Contains(body, `data-type="custom"`) || !strings.Contains(body, "Update link fields") {
		t.Error("the item must carry its type for CSS and offer the no-JS refresh")
	}
	css, err := os.ReadFile("../../web/admin/css/pages/menus.css")
	if err != nil || strings.Contains(string(css), "[data-hm-ready] .hm-menutree__item") {
		t.Errorf("hiding of unused targets must not depend on JavaScript: %v", err)
	}
}

func TestPickerWantsAnExactNameOrSlug(t *testing.T) {
	t.Parallel()

	g, fm := newMenuRig(t)
	m := fm.add("Main", "")
	f := itemForm("Main", [4]string{"Contact", "entity", "0", ""})
	f.Set("i.0.entity_q", "Cont") // a partial name must not pick "Contact"
	if rec := g.do("POST", "/admin/theme/menus/"+m.ID.String(), "owner-token", f, nil); rec.Code != 422 || len(fm.replaced) != 0 {
		t.Errorf("a partial name must not resolve: %d", rec.Code)
	}
}

func TestMenuNoLinkAndMissingTargetsSave(t *testing.T) {
	t.Parallel()

	g, fm := newMenuRig(t)
	m := fm.add("Main", "footer")
	f := itemForm("Main", [4]string{"Product", "none", "0", ""}, [4]string{"Page", "entity", "1", ""},
		[4]string{"Address", "custom", "1", ""}, [4]string{"Archive", "archive", "1", ""})
	rec := g.do("POST", "/admin/theme/menus/"+m.ID.String(), "owner-token", f, nil)
	if rec.Code != 303 || len(fm.replaced) != 1 {
		t.Fatalf("items without a target must save: %d %.300s", rec.Code, rec.Body.String())
	}
	items := fm.replaced[0].Items
	wantType := []string{menu.LinkTypeNone, menu.LinkTypeEntity, menu.LinkTypeCustom, menu.LinkTypeArchive}
	for i, it := range items {
		if it.LinkType != wantType[i] || it.EntityID != nil || it.URL != nil {
			t.Errorf("item %d stored as %s with a target: %+v", i, it.LinkType, it)
		}
	}
	// Reloaded, the editor keeps the type and warns about the three that lack a target, not the No-link one.
	rows := []menuRow{}
	flattenMenu([]*menu.MenuItem{
		{Label: "Product", LinkType: menu.LinkTypeNone},
		{Label: "Page", LinkType: menu.LinkTypeEntity},
		{Label: "Address", LinkType: menu.LinkTypeCustom},
		{Label: "Archive", LinkType: menu.LinkTypeArchive},
	}, 0, &rows)
	for i, want := range []struct{ typ, warn string }{{"none", ""}, {"entity", menuNoLinkWarning}, {"custom", menuNoLinkWarning}, {"archive", menuNoLinkWarning}} {
		if rows[i].Type != want.typ || rows[i].Warn != want.warn {
			t.Errorf("row %d: type %q warn %q, want %q %q", i, rows[i].Type, rows[i].Warn, want.typ, want.warn)
		}
	}
	m2 := fm.add("Other", "")
	m2.Items = []*menu.MenuItem{{Label: "Page", LinkType: menu.LinkTypeEntity}}
	body := g.do("GET", "/admin/theme/menus/"+m2.ID.String(), "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, `data-type="entity"`) {
		t.Errorf("an entity item without a page must reload as a page item: %.200s", body)
	}
	// A malformed address still blocks the save.
	bad := itemForm("Main", [4]string{"Bad", "custom", "0", "javascript:x"})
	if rec := g.do("POST", "/admin/theme/menus/"+m.ID.String(), "owner-token", bad, nil); rec.Code != 422 || len(fm.replaced) != 1 {
		t.Errorf("a malformed address must block: %d", rec.Code)
	}
}

func TestMenuSavesClassesAndSettings(t *testing.T) {
	t.Parallel()

	g, fm := newMenuRig(t)
	m := fm.add("Main", "")
	f := itemForm("Main", [4]string{"Start", "custom", "0", "/start"})
	for k, v := range map[string]string{"class": "footer-cta   extra", "style": "button", "color": "accent", "badge": "new",
		"icon": "arrow", "show": "mobile", "id": "cta", "title": "Begin", "aria": "Get started"} {
		f.Set("i.0."+k, v)
	}
	f["i.0.rel"] = []string{"nofollow", "ugc"}
	if rec := g.do("POST", "/admin/theme/menus/"+m.ID.String(), "owner-token", f, nil); rec.Code != 303 {
		t.Fatalf("save: %d %.300s", rec.Code, rec.Body.String())
	}
	it := fm.replaced[0].Items[0]
	want := menu.MenuItemAttrs{Style: "button", Color: "accent", Badge: "new", Icon: "arrow", ShowOn: "mobile", ID: "cta", Title: "Begin", AriaLabel: "Get started", Rel: []string{"nofollow", "ugc"}}
	if it.CSSClasses == nil || *it.CSSClasses != "footer-cta extra" || it.Attrs.Style != want.Style || it.Attrs.Color != want.Color ||
		it.Attrs.Badge != want.Badge || it.Attrs.Icon != want.Icon || it.Attrs.ShowOn != want.ShowOn || it.Attrs.ID != want.ID ||
		it.Attrs.Title != want.Title || it.Attrs.AriaLabel != want.AriaLabel || strings.Join(it.Attrs.Rel, ",") != "nofollow,ugc" {
		t.Errorf("settings not carried: %+v classes=%v", it.Attrs, it.CSSClasses)
	}
	// "All devices" is stored as nothing.
	f.Set("i.0.show", "all")
	g.do("POST", "/admin/theme/menus/"+m.ID.String(), "owner-token", f, nil)
	if got := fm.replaced[1].Items[0].Attrs.ShowOn; got != "" {
		t.Errorf("show on all stored as %q", got)
	}
	// Each bad value is refused on its own field and nothing is saved.
	for field, val := range map[string]string{"class": `a"b`, "style": "Big Button", "show": "tablet", "id": "1x", "rel": "evil", "title": strings.Repeat("t", 101)} {
		bad := itemForm("Main", [4]string{"Start", "custom", "0", "/start"})
		bad.Set("i.0."+field, val)
		rec := g.do("POST", "/admin/theme/menus/"+m.ID.String(), "owner-token", bad, nil)
		if rec.Code != 422 || len(fm.replaced) != 2 {
			t.Errorf("%s=%q: want 422 and no save, got %d (saved %d)", field, val, rec.Code, len(fm.replaced))
		}
	}
}

// A structural edit rebuilds the rows from the post; the warning of an item
// without a link, the copy's HTML id and the Add select must still be right.
func TestMenuOpsKeepWarningsAndDropDuplicateIDs(t *testing.T) {
	t.Parallel()

	g, fm := newMenuRig(t)
	m := fm.add("Main", "")
	f := itemForm("Main", [4]string{"Pricing", "custom", "0", ""})
	f.Set("i.0.id", "cta")
	f.Set("_op", "dup.0")
	rec := g.do("POST", "/admin/theme/menus/"+m.ID.String(), "owner-token", f, nil)
	body := rec.Body.String()
	if rec.Code != 200 || strings.Count(body, "Without a link") != 2 {
		t.Errorf("duplicate: %d, want 2 warnings, got %d", rec.Code, strings.Count(body, "Without a link"))
	}
	if strings.Count(body, `name="i.0.id" value="cta"`) != 1 || strings.Contains(body, `name="i.1.id" value="cta"`) {
		t.Error("the copy must not carry the HTML id")
	}
	f = itemForm("Main")
	f.Set("add_type", "none")
	f.Set("_op", "add")
	body = g.do("POST", "/admin/theme/menus/"+m.ID.String(), "owner-token", f, nil).Body.String()
	if n := strings.Count(body, `>No link</option>`); n != 1 {
		t.Errorf("the Add select lists No link %d times", n)
	}
}

func TestMenuNoticeKind(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		op, target string
		in         []string
		want       string
	}{
		"refused indent warns":            {"indent.0", "", []string{"A:0", "B:0"}, "warning"},
		"refused move without target":     {"moveto.0", "", []string{"A:0", "B:0"}, "warning"},
		"refused move under itself warns": {"moveto.0", "0", []string{"A:0", "A1:1"}, "warning"},
		"applied indent is info":          {"indent.1", "", []string{"A:0", "B:0"}, "info"},
		"applied move is info":            {"moveto.0", "1", []string{"A:0", "B:0"}, "info"},
		"add is info":                     {"add", "", []string{"A:0"}, "info"},
		"refresh is info":                 {"refresh", "", []string{"A:0"}, "info"},
	}
	for name, tc := range tests {
		rows := treeRows(tc.in...)
		before := slices.Clone(rows)
		got, msg := applyMenuOp(rows, tc.op, "", tc.target)
		if k := menuNoticeKind(tc.op, msg, before, got); k != tc.want {
			t.Errorf("%s: kind %q (%q), want %q", name, k, msg, tc.want)
		}
	}
	rows := treeRows("A:0", "B:0")
	if _, msg := applyMenuOp(rows, "moveto.0", "", ""); msg != "Choose where to move this item, then use Move to again." {
		t.Errorf("moveto message: %q", msg)
	}
}

func TestMenuRefusedOpRendersAWarningCallout(t *testing.T) {
	t.Parallel()

	g, fm := newMenuRig(t)
	m := fm.add("Main", "")
	form := itemForm("Main", [4]string{"A", "custom", "0", "/a"}, [4]string{"B", "custom", "0", "/b"})
	for op, want := range map[string]string{"indent.0": "hm-callout--warning", "outdent.1": "hm-callout--warning", "down.1": "hm-callout--warning", "dup.0": "hm-callout--info"} {
		f := url.Values{}
		for k, v := range form {
			f[k] = v
		}
		f.Set("_op", op)
		body := g.do("POST", "/admin/theme/menus/"+m.ID.String(), "owner-token", f, nil).Body.String()
		if !strings.Contains(body, want) {
			t.Errorf("%s: want %s in the notice", op, want)
		}
	}
}

func TestMenuOpsRefuseAtTheItemLimit(t *testing.T) {
	t.Parallel()

	full := make([]string, maxMenuItems)
	for i := range full {
		full[i] = "X:0"
	}
	for _, op := range []string{"add", "dup.0"} {
		rows := treeRows(full...)
		got, msg := applyMenuOp(rows, op, "", "")
		if msg != "A menu holds at most 200 items." || len(got) != maxMenuItems {
			t.Errorf("%s at the limit: %d rows, %q", op, len(got), msg)
		}
		if k := menuNoticeKind(op, msg, rows, got); k != "warning" {
			t.Errorf("%s at the limit: kind %q, want warning", op, k)
		}
	}
	if _, msg := applyMenuOp(treeRows(full[1:]...), "add", "", ""); strings.Contains(msg, "at most") {
		t.Errorf("add below the limit refused: %q", msg)
	}
}
