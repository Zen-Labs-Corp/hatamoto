package admin

import (
	"flag"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/admin/ui"
)

var update = flag.Bool("update", false, "rewrite golden files")

func compareGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/admin -update)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs from golden (run with -update to accept):\n--- want\n%s\n--- got\n%s", name, want, got)
	}
}

// checkPartial renders a partial, compares it to its golden and runs the
// accessibility checker on it.
func checkPartial(t *testing.T, a *Admin, partial, caseName string, data any) string {
	t.Helper()
	out, err := a.RenderPartial(partial, data)
	if err != nil {
		t.Fatalf("%s/%s: %v", partial, caseName, err)
	}
	s := strings.TrimSpace(string(out)) + "\n"
	compareGolden(t, "partials/"+strings.ReplaceAll(partial, "/", "_")+"."+caseName+".html", s)
	if issues := a11y.Check(s, a11y.Options{}); len(issues) > 0 {
		t.Errorf("%s/%s a11y: %s", partial, caseName, strings.Join(issues, "; "))
	}
	return s
}

func newPartialAdmin(t *testing.T) *Admin { return newRig(t).a }

func TestPartialButton(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	checkPartial(t, a, "hm/button", "default", map[string]any{"Label": "Cancel"})
	checkPartial(t, a, "hm/button", "primary-submit", map[string]any{"Label": "Save", "Variant": "primary", "Type": "submit"})
	checkPartial(t, a, "hm/button", "danger-small", ui.Button{Label: "Delete", Variant: "danger", Size: "sm"}.Dict())
	checkPartial(t, a, "hm/button", "link", map[string]any{"Label": "Edit", "Href": "/admin/x", "Icon": "edit"})
	checkPartial(t, a, "hm/button", "icon-only", ui.Button{Icon: "trash", IconOnly: true, AriaLabel: "Delete user", Variant: "ghost"}.Dict())
	checkPartial(t, a, "hm/button", "htmx", ui.Button{Label: "Reload", Attrs: map[string]string{"hx-get": "/admin/users", "hx-target": "#list", "data-x": `a"b`}}.Dict())
	checkPartial(t, a, "hm/button", "disabled", map[string]any{"Label": "Save", "Attrs": map[string]string{"disabled": "disabled"}})
}

func TestPartialButtonRejectsUnsafeInput(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	for name, d := range map[string]map[string]any{
		"icon-only without a name": {"Icon": "x", "IconOnly": true},
		"inline code attribute":    {"Label": "x", "Attrs": map[string]string{"hx-on:click": "alert(1)"}},
		"onclick":                  {"Label": "x", "Attrs": map[string]string{"onclick": "alert(1)"}},
		"hx-vals":                  {"Label": "x", "Attrs": map[string]string{"hx-vals": "js:{a:1}"}},
	} {
		if _, err := a.RenderPartial("hm/button", d); err == nil {
			t.Errorf("%s: expected a render error", name)
		}
	}
}

// TestAttrsWhitelistPlainHTML covers the plain HTML attributes added for
// popover triggers, hidden controls and downloads, and that handlers stay out.
func TestAttrsWhitelistPlainHTML(t *testing.T) {
	t.Parallel()

	got, err := attrs(map[string]string{"popovertarget": "p", "popovertargetaction": "hide", "hidden": "", "download": ""})
	if err != nil {
		t.Fatal(err)
	}
	want := ` download="" hidden="" popovertarget="p" popovertargetaction="hide"`
	if string(got) != want {
		t.Errorf("attrs = %q, want %q", got, want)
	}
	for _, k := range []string{"onclick", "onpopovertoggle", "hx-on:click", "popoverTargetx", "hiddenx", "srcdoc"} {
		if _, err := attrs(map[string]string{k: "x"}); err == nil {
			t.Errorf("%s must stay rejected", k)
		}
	}
}

func TestPartialSelect(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	opts := []map[string]any{{"Value": "a", "Label": "Draft"}, {"Value": "b", "Label": "Hero · Big headline", "Selected": true}}
	s := checkPartial(t, a, "hm/select", "default", map[string]any{"ID": "kind", "Name": "kind", "AriaLabel": "Kind", "Options": opts})
	for _, want := range []string{`data-hm-select`, `<select class="hm-field__control" id="kind" name="kind" aria-label="Kind">`, `<option value="b" selected>`} {
		if !strings.Contains(s, want) {
			t.Errorf("default select lacks %q: %s", want, s)
		}
	}
	s = checkPartial(t, a, "hm/select", "placeholder", map[string]any{"ID": "kind", "Name": "kind", "AriaLabel": "Kind", "Placeholder": "Choose a kind", "Class": "hm-bo__kind", "Auto": true, "Options": opts[:1]})
	if !strings.Contains(s, `<option value="">Choose a kind</option>`) || !strings.Contains(s, "hm-select--auto") || !strings.Contains(s, "hm-field__control hm-bo__kind") {
		t.Errorf("placeholder select: %s", s)
	}
	s = checkPartial(t, a, "hm/select", "disabled", map[string]any{"ID": "kind", "Name": "kind", "AriaLabel": "Kind", "Disabled": true, "Options": opts})
	if !strings.Contains(s, " disabled>") {
		t.Errorf("disabled select: %s", s)
	}
	s = checkPartial(t, a, "hm/select", "required", map[string]any{"ID": "kind", "Name": "kind", "AriaLabel": "Kind", "Required": true, "Attrs": map[string]string{"data-hm-kind": ""}, "Options": opts})
	for _, want := range []string{" required", `data-hm-kind`} {
		if !strings.Contains(s, want) {
			t.Errorf("required select lacks %q: %s", want, s)
		}
	}
	out, err := a.RenderPartial("hm/select", map[string]any{"ID": "kind", "Name": "kind", "AriaLabel": "Kind", "Invalid": true, "DescribedBy": "kind-error", "Options": opts})
	if err != nil || !strings.Contains(string(out), `aria-invalid="true" aria-describedby="kind-error"`) {
		t.Errorf("invalid wiring: %v %s", err, out)
	}
	if _, err := a.RenderPartial("hm/select", map[string]any{"Name": "x"}); err == nil {
		t.Error("hm/select without an ID must fail")
	}
}

func TestPartialField(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	checkPartial(t, a, "hm/field", "text", map[string]any{"ID": "email", "Name": "email", "Label": "Email", "Type": "email", "Value": "a@b.c"})
	checkPartial(t, a, "hm/field", "required-help", map[string]any{"ID": "name", "Name": "name", "Label": "Name", "Required": true, "Help": "Shown to editors"})
	s := checkPartial(t, a, "hm/field", "error", map[string]any{"ID": "pw", "Name": "pw", "Label": "Password", "Type": "password", "Required": true, "Help": "12+ characters", "Error": "Use at least 12 characters"})
	if !strings.Contains(s, `aria-invalid="true"`) || !strings.Contains(s, `aria-describedby="pw-help pw-error"`) {
		t.Errorf("error wiring: %s", s)
	}
	checkPartial(t, a, "hm/field", "textarea", map[string]any{"ID": "bio", "Name": "bio", "Label": "Bio", "Control": "textarea", "Value": "<b>hi</b>", "Rows": 3})
	checkPartial(t, a, "hm/field", "select", map[string]any{"ID": "role", "Name": "role", "Label": "Role", "Control": "select",
		"Options": []ui.Option{{Value: "owner", Label: "Owner"}, {Value: "editor", Label: "Editor", Selected: true}}})
	checkPartial(t, a, "hm/field", "checkbox", map[string]any{"ID": "tos", "Name": "tos", "Label": "I agree", "Control": "checkbox", "Checked": true})
	checkPartial(t, a, "hm/field", "switch", map[string]any{"ID": "on", "Name": "on", "Label": "Enabled", "Control": "switch"})
	checkPartial(t, a, "hm/field", "radio", map[string]any{"ID": "lang", "Name": "lang", "Label": "Starter content", "Control": "radio",
		"Options": []ui.Option{{Value: "empty", Label: "Start empty", Selected: true}, {Value: "starter", Label: "Install starter content"}}})
	if _, err := a.RenderPartial("hm/field", map[string]any{"Name": "x", "Label": "x"}); err == nil {
		t.Error("a field without an ID must not render")
	}
}

func renderField(t *testing.T, a *Admin, data map[string]any) string {
	t.Helper()
	out, err := a.RenderPartial("hm/field", data)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out)) + "\n"
}

func TestPartialSwitchBareOffValue(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	s := renderField(t, a, map[string]any{"ID": "perf-enabled", "Name": "enabled", "Value": "on", "Label": "Page cache",
		"Control": "switch", "Bare": true, "OffValue": "off", "Checked": true})
	box := strings.Index(s, `type="checkbox"`)
	sentinel := strings.Index(s, `<input type="hidden" name="enabled" value="off">`)
	if box < 0 || sentinel < box || sentinel < strings.Index(s, "</label>") {
		t.Errorf("sentinel must follow the switch label: %s", s)
	}
	if strings.Contains(s, "hm-visually-hidden") {
		t.Errorf("bare switch must add no hidden duplicate label: %s", s)
	}
	d := renderField(t, a, map[string]any{"ID": "perf-preload", "Name": "preload", "Value": "on", "Label": "Preload",
		"Control": "switch", "Bare": true, "OffValue": "off", "Disabled": true})
	if !strings.Contains(d, `value="on" disabled>`) || !strings.Contains(d, `<input type="hidden" name="preload" value="off" disabled>`) {
		t.Errorf("disabled must apply to the switch and its sentinel: %s", d)
	}
}

func TestPartialCardFoot(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	start, _ := a.RenderPartial("hm/card-start", map[string]any{"ID": "c", "Title": "T"})
	foot, err := a.RenderPartial("hm/card-foot", nil)
	if err != nil {
		t.Fatal(err)
	}
	end, err := a.RenderPartial("hm/card-foot-end", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(foot), `</div><footer class="hm-card__foot">`) || !strings.Contains(string(end), `</footer></section>`) {
		t.Errorf("card foot markup: %s %s", foot, end)
	}
	if issues := a11y.Check(string(start)+"<p>x</p>"+string(foot)+"<button type=\"submit\">Save</button>"+string(end), a11y.Options{}); len(issues) > 0 {
		t.Errorf("card foot pair: %v", issues)
	}
	plain, _ := a.RenderPartial("hm/foot", nil)
	plainEnd, _ := a.RenderPartial("hm/foot-end", nil)
	if strings.TrimSpace(string(plain)) != `<footer class="hm-card__foot hm-card__foot--plain">` || strings.TrimSpace(string(plainEnd)) != `</footer>` {
		t.Errorf("plain foot markup: %s %s", plain, plainEnd)
	}
}

// Plain and Mono drop the dot only for a neutral label; a coloured status keeps it (colour alone is not enough, WCAG 1.4.1).
func TestBadgePlainKeepsTheDotOfAStatus(t *testing.T) {
	a := newPartialAdmin(t)
	for _, tc := range []struct {
		b       ui.Badge
		wantDot bool
	}{
		{ui.Badge{Label: "Training", Plain: true}, false},
		{ui.Badge{Label: "Training", Kind: "neutral", Plain: true}, false},
		{ui.Badge{Label: "hook", Mono: true}, false},
		{ui.Badge{Label: "Failed", Kind: "danger", Plain: true}, true},
		{ui.Badge{Label: "Failed", Kind: "warning", Mono: true}, true},
	} {
		out, err := a.RenderPartial("hm/badge", tc.b)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(string(out), "hm-badge__dot"); got != tc.wantDot {
			t.Errorf("%+v: dot = %v, want %v: %s", tc.b, got, tc.wantDot, out)
		}
	}
}

// A radio group without a Name would post nothing and fail silently; item Attrs reach the control in every mode.
func TestSegmentedRequiresNameAndHonoursAttrs(t *testing.T) {
	a := newPartialAdmin(t)
	items := []ui.SegmentedItem{{Label: "A", Value: "a", Pressed: true, Attrs: map[string]string{"data-hook": "x"}}, {Label: "B", Value: "b"}}
	if _, err := a.RenderPartial("hm/segmented", ui.Segmented{Mode: "radio", Label: "Pick", Items: items}); err == nil {
		t.Error("a radio segmented without Name rendered")
	}
	for _, mode := range []string{"", "radio", "buttons"} {
		out, err := a.RenderPartial("hm/segmented", ui.Segmented{Mode: mode, Label: "Pick", Name: "pick", Action: "/x", Attrs: map[string]string{"data-box": "y"}, Items: items})
		if err != nil {
			t.Fatalf("mode %q: %v", mode, err)
		}
		if got := strings.Count(string(out), `data-hook="x"`); got != 1 {
			t.Errorf("mode %q: item attrs rendered %d times: %s", mode, got, out)
		}
		if !strings.Contains(string(out), `data-box="y"`) {
			t.Errorf("mode %q: container attrs missing: %s", mode, out)
		}
	}
}

func TestPartialSmall(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	for _, k := range []string{"neutral", "success", "warning", "danger", "info"} {
		checkPartial(t, a, "hm/badge", k, ui.Badge{Label: "Status " + k, Kind: k})
	}
	checkPartial(t, a, "hm/tabs", "default", map[string]any{"Label": "Entity types", "Items": []ui.Tab{{Label: "All", Href: "?", Active: true, Count: 12}, {Label: "Drafts", Href: "?status=draft"}}})
	checkPartial(t, a, "hm/empty", "default", ui.Empty{Icon: "inbox", Title: "No forms yet", Body: "Forms collect enquiries.", Actions: []ui.Button{{Label: "New form", Href: "/admin/forms/new", Variant: "primary"}}})
	checkPartial(t, a, "hm/errorpage", "retry", map[string]any{"Message": "Database unreachable", "RetryURL": "/admin/users"})
	checkPartial(t, a, "hm/formerrors", "two", map[string]any{"Errors": []ui.FieldError{{ID: "email", Label: "Email", Message: "Enter a valid email address"}, {ID: "name", Label: "Name", Message: "Name is required"}}})
	checkPartial(t, a, "hm/flash", "success", &Flash{Kind: "success", Message: "Saved"})
	checkPartial(t, a, "hm/flash", "error", &Flash{Kind: "error", Message: "Could not save"})
	inline := checkPartial(t, a, "hm/flash", "inline", map[string]any{"Kind": "error", "Message": "Could not import", "Inline": true})
	if strings.Contains(inline, "data-hm-flash") {
		t.Errorf("inline flash would become a toast: %s", inline)
	}
	if out, err := a.RenderPartial("hm/flash", map[string]any{"Kind": "error", "Message": ""}); err != nil || strings.TrimSpace(string(out)) != "" {
		t.Errorf("empty flash message rendered %q (err %v)", out, err)
	}
	checkPartial(t, a, "hm/mediatile", "selectable", map[string]any{"ID": "m1", "URL": "/media/a.png", "Thumb": "/media/a-thumb.png", "Alt": "Team photo", "Filename": "a.png", "Meta": "120 KB", "Selectable": true, "Selected": true})
	checkPartial(t, a, "hm/mediatile", "decorative", map[string]any{"ID": "m2", "URL": "/media/b.png", "Filename": "b.png", "Decorative": true})
}

func TestPartialCardDialogPageheader(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	start := checkPartial(t, a, "hm/card-start", "titled", map[string]any{"ID": "profile", "Title": "Profile", "Description": "Your details", "Heading": "h2"})
	if !strings.Contains(start, `aria-labelledby="profile-title"`) {
		t.Errorf("card not labelled: %s", start)
	}
	// start+end form a balanced fragment
	pair, _ := a.RenderPartial("hm/card-start", map[string]any{"ID": "c", "Title": "T"})
	end, _ := a.RenderPartial("hm/card-end", nil)
	if issues := a11y.Check(string(pair)+"<p>x</p>"+string(end), a11y.Options{}); len(issues) > 0 {
		t.Errorf("card pair: %v", issues)
	}
	d1 := checkPartial(t, a, "hm/dialog-start", "danger", map[string]any{"ID": "del", "Title": "Delete user?", "Danger": true})
	d2 := checkPartial(t, a, "hm/dialog-end", "default", map[string]any{})
	if issues := a11y.Check(d1+"<p>Sure?</p>"+d2, a11y.Options{}); len(issues) > 0 {
		t.Errorf("dialog pair: %v", issues)
	}
	checkPartial(t, a, "hm/pageheader", "full", map[string]any{
		"CSRF": "tok", "Title": "Users", "Description": "People who can sign in",
		"Actions":  []ui.Button{{Label: "Invite user", Variant: "primary", Href: "/admin/users/invite"}},
		"Overflow": []ui.Action{{Label: "Export", Href: "/admin/users.csv"}, {Label: "Purge", Post: "/admin/users/purge", Danger: true, Fields: map[string]string{"scope": "all"}}},
	})
	checkPartial(t, a, "hm/pageheader", "title-only", map[string]any{"Title": "Dashboard"})
}

func TestPartialPagination(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	q := url.Values{"q": {"jo"}, "sort": {"name"}}
	checkPartial(t, a, "hm/pagination", "first", ui.Pagination{Page: 1, PerPage: 25, Total: 120, BaseURL: "/admin/users", Query: q})
	s := checkPartial(t, a, "hm/pagination", "middle", ui.Pagination{Page: 5, PerPage: 10, Total: 200, BaseURL: "/admin/users", Query: q})
	for _, want := range []string{`rel="prev"`, `rel="next"`, `aria-current="page"`, "Showing 41–50 of 200", "q=jo", "…"} {
		if !strings.Contains(s, want) {
			t.Errorf("middle page missing %q:\n%s", want, s)
		}
	}
	checkPartial(t, a, "hm/pagination", "last", ui.Pagination{Page: 3, PerPage: 25, Total: 60, BaseURL: "/admin/users"})
}

func fixtureTable() ui.Table {
	q := url.Values{"q": {"jo"}, "role": {"editor"}}
	return ui.Table{
		ID: "users", Caption: "Users", BaseURL: "/admin/users", CSRF: "tok", BulkURL: "/admin/users/bulk",
		Columns: []ui.Col{{Key: "name", Label: "Name", Sortable: true}, {Key: "email", Label: "Email", Sortable: true}, {Key: "role", Label: "Role"}, {Key: "last", Label: "Last sign-in", Align: "right"}},
		Rows: []ui.Row{
			{ID: "u1", Label: "Jo Ng", Cells: []ui.Cell{{Text: "Jo Ng", Link: "/admin/users/u1"}, {Text: "jo@example.com"}, {Badge: &ui.Badge{Label: "Owner", Kind: "info"}}, {Text: "2 days ago"}},
				Actions: []ui.Action{{Label: "Edit", Href: "/admin/users/u1"}, {Label: "Delete", Post: "/admin/users/u1/delete", Danger: true}}},
			{ID: "u2", Label: "Jonas <b>", Cells: []ui.Cell{{Text: "Jonas <b>"}, {Text: "jonas@example.com"}, {Badge: &ui.Badge{Label: "Editor", Kind: "neutral"}}, {Text: "never"}}},
		},
		SortKey: "name", SortDir: "asc", Query: "jo", SearchLabel: "Search users",
		Filters: []ui.Filter{{Name: "role", Label: "Role", Options: []ui.Option{{Value: "", Label: "All roles"}, {Value: "editor", Label: "Editor", Selected: true}}}},
		Bulk:    []ui.BulkAction{{Label: "Delete selected", Action: "/admin/users/bulk", Value: "delete", Danger: true}},
		Page:    ui.Pagination{Page: 1, PerPage: 2, Total: 5, BaseURL: "/admin/users", Query: q},
	}
}

func TestPartialTable(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	s := checkPartial(t, a, "hm/table", "full", fixtureTable())
	for _, want := range []string{
		`aria-sort="ascending"`, `aria-sort="none"`, `href="/admin/users?dir=desc&amp;q=jo&amp;role=editor&amp;sort=name"`,
		`data-hm-select-all`, `id="row-u1"`, `aria-label="Select Jo Ng"`, `role="search"`, `hx-select="#users"`, `form="bulk-users"`,
		`Jonas &lt;b&gt;`, `<th scope="row"`, "Showing 1–2 of 5", `aria-label="Actions for Jo Ng"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("table missing %q", want)
		}
	}
	empty := fixtureTable()
	empty.Rows = nil
	empty.Empty = ui.Empty{Icon: "users", Title: "No users match", Body: "Try another search."}
	checkPartial(t, a, "hm/table", "empty", empty)
	broken := fixtureTable()
	broken.Error = "The database did not answer."
	checkPartial(t, a, "hm/table", "error", broken)
	// the active sort flips; another column starts ascending
	tb := fixtureTable()
	if !strings.Contains(tb.SortHref("email"), "dir=asc") || !strings.Contains(tb.SortHref("name"), "dir=desc") {
		t.Errorf("sort links: %s | %s", tb.SortHref("email"), tb.SortHref("name"))
	}
}

// A row menu lists links and forms as menuitems, the destructive one last
// behind a separator, and its button names the row.
func TestRowMenuOrder(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	tb := fixtureTable()
	tb.Rows[0].Actions = []ui.Action{
		{Label: "Edit", Href: "/admin/users/u1"},
		{Label: "Delete", Post: "/admin/users/u1/delete", Danger: true},
		{Label: "Duplicate", Post: "/admin/users/u1/copy", Icon: "copy"},
	}
	s := checkPartial(t, a, "hm/table", "rowmenu", tb)
	menu := s[strings.Index(s, `id="menu-users-u1"`):]
	menu = menu[:strings.Index(menu, "</tr>")]
	if n := strings.Count(menu, `role="menuitem"`); n != 3 {
		t.Fatalf("want 3 menuitems, got %d:\n%s", n, menu)
	}
	edit, dup, sep, del := strings.Index(menu, ">Edit<"), strings.Index(menu, ">Duplicate<"), strings.Index(menu, `role="separator"`), strings.Index(menu, ">Delete<")
	if !(edit < dup && dup < sep && sep < del) {
		t.Errorf("order wrong (edit %d, duplicate %d, separator %d, delete %d):\n%s", edit, dup, sep, del, menu)
	}
	if !strings.Contains(s, `aria-label="Actions for Jo Ng"`) || !strings.Contains(s, `popovertarget="menu-users-u1"`) {
		t.Errorf("menu button missing name or target:\n%s", s)
	}
}

// A row button is a link: it needs an Href, and Danger gives it the danger variant.
func TestRowButton(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	render := func(act ui.Action) (string, error) {
		out, err := a.RenderPartial("hm/rowbutton", act)
		return string(out), err
	}
	plain, err := render(ui.Action{Button: true, Label: "Open", Href: "/x"})
	if err != nil || !strings.Contains(plain, "<a ") || !strings.Contains(plain, `href="/x"`) || !strings.Contains(plain, "hm-btn--secondary") || strings.Contains(plain, "hm-btn--danger") {
		t.Errorf("plain row button = %q, %v", plain, err)
	}
	danger, err := render(ui.Action{Button: true, Label: "Unlink", Danger: true, Confirm: "Sure?", Href: "/x"})
	if err != nil || !strings.Contains(danger, "hm-btn--danger-text") || !strings.Contains(danger, "data-hm-danger") {
		t.Errorf("danger row button = %q, %v", danger, err)
	}
	if _, err := render(ui.Action{Button: true, Label: "Open"}); err == nil || !strings.Contains(err.Error(), "hm/rowbutton Href") {
		t.Errorf("row button without Href: err = %v, want hm/rowbutton Href", err)
	}
}

var (
	hashRx     = regexp.MustCompile(`(admin|[a-z]+)\.[0-9a-f]{12}\.(css|js)`)
	mapRx      = regexp.MustCompile(`(?s)(<script type="importmap"[^>]*>).*?(</script>)`)
	preloadSRI = regexp.MustCompile(`(<link rel="modulepreload" href="[^"]*" integrity=")[^"]*`)
	nonceRx    = regexp.MustCompile(`nonce="[^"]*"`)
	cfgNonce   = regexp.MustCompile(`(inline(?:Script|Style)Nonce&#34;:&#34;)[^&]*`)
	csrfMeta   = regexp.MustCompile(`name="csrf-token" content="[^"]*"`)
	userAttr   = regexp.MustCompile(`data-hm-user="[^"]*"`)
	// Static option lists (languages, time zones) are covered by unit tests;
	// collapsing them keeps the golden readable.
	optionList = regexp.MustCompile(`(?s)(<select[^>]*name="(?:language|timezone)"[^>]*>).*?(</select>)`)
)

// normalizePage removes what changes per build or per request.
func normalizePage(s string) string {
	s = mapRx.ReplaceAllString(s, "$1{…}$2")
	s = optionList.ReplaceAllString(s, "$1{options}$2")
	s = hashRx.ReplaceAllString(s, "$1.HASH.$2")
	s = preloadSRI.ReplaceAllString(s, "${1}sha256-SRI")
	s = nonceRx.ReplaceAllString(s, `nonce="NONCE"`)
	s = cfgNonce.ReplaceAllString(s, "${1}NONCE")
	s = csrfMeta.ReplaceAllString(s, `name="csrf-token" content="CSRF"`)
	s = userAttr.ReplaceAllString(s, `data-hm-user="USER"`)
	return s
}

func TestPageGoldensAndA11y(t *testing.T) {
	t.Parallel()

	g := newRig(t)
	for name, tc := range map[string]struct{ token, path string }{
		"shell-owner-404":  {"owner-token", "/admin/missing"},
		"shell-editor-404": {"editor-token", "/admin/missing"},
		"bare-404":         {"", "/admin/missing"},
	} {
		var body string
		if tc.token == "" {
			// no session: the bare error page comes from an error outside a session
			req := httptestReq("GET", tc.path, "")
			rec := httptestRec()
			g.a.Error(rec, req.WithContext(ctxWithNonce(req)), 404, "Page not found", "That address does not exist in the admin.")
			body = rec.Body.String()
		} else {
			body = g.do("GET", tc.path, tc.token, nil, nil).Body.String()
		}
		body = normalizePage(body)
		compareGolden(t, "pages/"+name+".html", body)
		if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
			t.Errorf("%s a11y: %s", name, strings.Join(issues, "; "))
		}
	}
}

// TestEveryViewHasWidth: the shell wraps every page in .hm-page with one of
// the page widths; anything else falls back to wide.
func TestEveryViewHasWidth(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{"": "wide", "wide": "wide", "narrow": "narrow", "compact": "compact", "full": "full", "huge": "wide"} {
		if got := pageWidth(in); got != want {
			t.Errorf("pageWidth(%q) = %q, want %q", in, got, want)
		}
	}
	rg := newRig(t)
	if body := rg.do("GET", "/admin/entities", "owner-token", nil, nil).Body.String(); !strings.Contains(body, `<div class="hm-page" data-width="wide">`) {
		t.Error("a shell page without a Width lacks the default wide .hm-page wrapper")
	}
	for _, w := range []string{"narrow", "compact", "full"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/admin/profile", nil)
		v := rg.a.NewView(rec, req, "T")
		v.Width, v.Data = w, profileData{}
		rg.a.Render(rec, req, http.StatusOK, "profile/index", v)
		if want := `<div class="hm-page" data-width="` + w + `">`; !strings.Contains(rec.Body.String(), want) {
			t.Errorf("Width %q: %s missing from the rendered shell (status %d)", w, want, rec.Code)
		}
	}
}

func TestPartialSparkSplit(t *testing.T) {
	t.Parallel()

	a := newPartialAdmin(t)
	vals := []int64{1, 2, 1, 2, 1, 2, 1, 4, 3, 4, 3, 4, 3, 4}
	s := checkPartial(t, a, "hm/spark", "split", ui.SparkSplitOf(vals, 7))
	for _, w := range []string{"hm-spark__prev", "hm-spark__divider", "hm-viz--info"} {
		if !strings.Contains(s, w) {
			t.Errorf("split spark lacks %s", w)
		}
	}
	// The default spark draws neither the grey period nor the divider.
	if s := checkPartial(t, a, "hm/spark", "plain", ui.SparkOf(vals)); strings.Contains(s, "hm-spark__prev") || strings.Contains(s, "hm-spark__divider") {
		t.Error("plain spark carries split markup")
	}
}
