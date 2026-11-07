package admin

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/form"
	"hatamoto.dev/core/internal/webhook"
)

func TestFormTabsPages(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	fm := ff.addForm("Contact", contactFields...)
	base := "/admin/forms/design/" + fm.ID.String()
	for _, tab := range []string{"settings", "share"} {
		rec := g.do("GET", base+"/"+tab, "owner-token", nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", tab, rec.Code)
		}
		if issues := a11y.Check(rec.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
			t.Errorf("%s a11y: %v", tab, issues)
		}
		if n := strings.Count(rec.Body.String(), `class="hm-tabs__tab"`); n != 4 {
			t.Errorf("%s: tab strip has %d items, want 4", tab, n)
		}
	}
	for _, p := range []string{base + "/nope", "/admin/forms/design/" + uuid.NewString() + "/settings", "/admin/forms/design/" + uuid.NewString() + "/share"} {
		if rec := g.do("GET", p, "owner-token", nil, nil); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", p, rec.Code)
		}
	}
}

func TestFormSettingsBoot(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	fm := ff.addForm("Contact", contactFields...)
	ff.addSub(fm, form.SubmissionStatusSpam, time.Now(), map[string]any{"name": "Spammer"})
	ff.addSub(fm, form.SubmissionStatusNew, time.Now().Add(-time.Hour), map[string]any{"name": "Ada"})
	settings := g.do("GET", "/admin/forms/design/"+fm.ID.String()+"/settings", "owner-token", nil, nil).Body.String()
	for _, want := range []string{`data-hm-fd-boot`, `"mode":"settings"`, `"sample":{"name":"Ada"}`, `"webhooks":[]`, `Add webhook`} {
		if !strings.Contains(settings, want) {
			t.Errorf("settings boot lacks %s", want)
		}
	}
	editor := g.do("GET", "/admin/forms/design/"+fm.ID.String()+"/settings", "editor-token", nil, nil)
	if editor.Code != http.StatusOK {
		t.Fatalf("editor settings: %d", editor.Code)
	}
	for _, leak := range []string{`"webhooks"`, "siteAddress", "spamUrl", "Add webhook", "/settings/webhooks"} {
		if strings.Contains(editor.Body.String(), leak) {
			t.Errorf("an editor's Settings page carries %q, which only owners may see", leak)
		}
	}
	if build := g.do("GET", "/admin/forms/design/"+fm.ID.String(), "owner-token", nil, nil).Body.String(); strings.Contains(build, `"mode"`) || strings.Contains(build, `"sample"`) {
		t.Error("the Build boot carries Settings data")
	}
}

// The Settings page carries the Build editor bar and the controls the script binds.
func TestFormSettingsPageHasEditorBar(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	fm := ff.addForm("Contact", contactFields...)
	body := g.do("GET", "/admin/forms/design/"+fm.ID.String()+"/settings", "owner-token", nil, nil).Body.String()
	for _, hook := range []string{"data-hm-fd-title", "data-hm-fd-status", "data-hm-fd-state", "data-hm-fd-undo", "data-hm-fd-redo", "data-hm-fd-save"} {
		if !strings.Contains(body, hook) {
			t.Errorf("settings lacks the editor bar hook %s", hook)
		}
	}
	for _, want := range []string{`data-hm-ti`, `data-hm-fs="notify_email"`, `data-hm-fs-seg="retention_days"`, `data-hm-fs-token="{{name}}"`, `action="/admin/forms/design/` + fm.ID.String() + `/duplicate"`, `Email (the visitor)`} {
		if !strings.Contains(body, want) {
			t.Errorf("settings lacks %s", want)
		}
	}
	if strings.Contains(body, "View on site") || strings.Contains(body, `name="version"`) {
		t.Error("settings shows View on site or the old version field")
	}
}

func TestFormSettingsOldPostIsGone(t *testing.T) {
	t.Parallel()

	g, ff, _ := newFormRig(t)
	fm := ff.addForm("Contact", contactFields...)
	rec := g.do("POST", "/admin/forms/design/"+fm.ID.String()+"/settings", "owner-token", url.Values{"name": {"A"}}, nil)
	if rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusNotFound {
		t.Errorf("POST settings: %d, want 405/404", rec.Code)
	}
}

func submissionsTabRig(t *testing.T, mutate ...func(*Deps)) (g *rig, ff *fakeForms, fm *form.Form, base string) {
	t.Helper()
	g, ff, _ = newFormRig(t, mutate...)
	fm = ff.addForm("Contact", contactFields...)
	return g, ff, fm, "/admin/forms/design/" + fm.ID.String() + "/submissions"
}

func TestSubmissionsTabRendersWithUnreadCountAndActiveTab(t *testing.T) {
	t.Parallel()

	g, ff, fm, base := submissionsTabRig(t)
	now := time.Now()
	ff.addSub(fm, form.SubmissionStatusNew, now.Add(-time.Hour), map[string]any{"name": "Ada", "email": "ada@example.com"})
	ff.addSub(fm, form.SubmissionStatusRead, now.Add(-2*time.Hour), map[string]any{"name": "Bob"})
	rec := g.do("GET", base, "owner-token", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("tab: %d", rec.Code)
	}
	body := rec.Body.String()
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	if n := strings.Count(body, `class="hm-tabs__tab"`); n != 4 {
		t.Errorf("tab strip has %d items, want 4", n)
	}
	for _, want := range []string{`aria-current="page"><svg`, `<span class="hm-tabs__count">1</span>`, `hm-ss__row--unread`, `data-hm-ss-open`, `data-hm-ss-row`, `Ada`, `Bob`, `/forms/submissions/export.csv?form=` + fm.ID.String()} {
		if !strings.Contains(body, want) {
			t.Errorf("tab lacks %q", want)
		}
	}
	if strings.Contains(body, "data-hm-ss-autoopen") {
		t.Error("no submission is open, but the page asks to open the drawer")
	}
	for _, tab := range []string{"settings", "share"} {
		page := g.do("GET", "/admin/forms/design/"+fm.ID.String()+"/"+tab, "owner-token", nil, nil).Body.String()
		if !strings.Contains(page, `href="`+base+`"`) || !strings.Contains(page, `<span class="hm-tabs__count">1</span>`) {
			t.Errorf("%s tab strip must link the Submissions tab and show the unread count", tab)
		}
	}
	if rec := g.do("GET", "/admin/forms/design/"+uuid.NewString()+"/submissions", "owner-token", nil, nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown form: %d, want 404", rec.Code)
	}
}

func TestSubmissionsTabFiltersAndKeepsThemWhilePaging(t *testing.T) {
	t.Parallel()

	g, ff, fm, base := submissionsTabRig(t)
	now := time.Now()
	for i := range 30 {
		ff.addSub(fm, form.SubmissionStatusNew, now.Add(-time.Duration(i)*time.Minute), map[string]any{"name": "Match " + strconv.Itoa(i)})
	}
	ff.addSub(fm, form.SubmissionStatusSpam, now, map[string]any{"name": "Match spam"})
	ff.addSub(fm, form.SubmissionStatusNew, now, map[string]any{"name": "Other person"})
	body := g.do("GET", base+"?q=match&status=new", "owner-token", nil, nil).Body.String()
	if strings.Contains(body, "Other person") || strings.Contains(body, "Match spam") {
		t.Error("the filter lets non-matching rows through")
	}
	if n := strings.Count(body, "data-hm-ss-row"); n != submissionsPerPage {
		t.Errorf("page 1 has %d rows, want %d", n, submissionsPerPage)
	}
	if !strings.Contains(body, "30 submissions") {
		t.Error("the live region lacks the filtered total")
	}
	for _, want := range []string{`page=2`, `q=match`, `status=new`} {
		if !strings.Contains(body, want) {
			t.Errorf("pager lacks %q", want)
		}
	}
	page2 := g.do("GET", base+"?q=match&status=new&page=2", "owner-token", nil, nil).Body.String()
	if n := strings.Count(page2, "data-hm-ss-row"); n != 5 {
		t.Errorf("page 2 has %d rows, want 5", n)
	}
	if bad := g.do("GET", base+"?status=bogus", "owner-token", nil, nil).Body.String(); !strings.Contains(bad, "Other person") || !strings.Contains(bad, "Match spam") {
		t.Error("an unknown status must mean All")
	}
	// A htmx filter request gets the same table.
	frag := g.do("GET", base+"?q=other", "owner-token", nil, map[string]string{"HX-Request": "true", "HX-Target": "ss-results"})
	if frag.Code != http.StatusOK || !strings.Contains(frag.Body.String(), "Other person") || strings.Contains(frag.Body.String(), "<html") {
		t.Errorf("htmx filter: %d %.200s", frag.Code, frag.Body.String())
	}
}

func TestSubmissionsTabOpensDrawerAndMarksRead(t *testing.T) {
	t.Parallel()

	g, ff, fm, base := submissionsTabRig(t)
	sub := ff.addSub(fm, form.SubmissionStatusNew, time.Now(), map[string]any{"name": "Ada", "email": "ada@example.com"})
	other := ff.addForm("Other", contactFields...)
	foreign := ff.addSub(other, form.SubmissionStatusNew, time.Now(), map[string]any{"name": "Eve"})

	// A prefetch must not mark it read.
	if rec := g.do("GET", base+"?s="+sub.ID.String(), "owner-token", nil, map[string]string{"Sec-Purpose": "prefetch;prerender"}); rec.Code != http.StatusOK {
		t.Fatalf("prefetch: %d", rec.Code)
	}
	if ff.subs[sub.ID].Status != form.SubmissionStatusNew {
		t.Fatal("a prefetch marked the submission read")
	}
	rec := g.do("GET", base+"?s="+sub.ID.String(), "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || ff.subs[sub.ID].Status != form.SubmissionStatusRead {
		t.Fatalf("open: %d, status %s", rec.Code, ff.subs[sub.ID].Status)
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("drawer a11y: %v", issues)
	}
	for _, want := range []string{"data-hm-ss-autoopen", `data-hm-ss-id="` + sub.ID.String() + `"`, "Not answered", `href="mailto:ada@example.com"`, `/forms/submissions/` + sub.ID.String() + `/status`, `/forms/submissions/` + sub.ID.String() + `/delete`, `aria-selected="true"`} {
		if !strings.Contains(body, want) {
			t.Errorf("drawer lacks %q", want)
		}
	}
	if strings.Contains(body, "hm-ss__row--unread") {
		t.Error("the opened row still shows as unread")
	}
	// The drawer script gets the fragment alone.
	frag := g.do("GET", base+"?s="+sub.ID.String(), "owner-token", nil, map[string]string{"HX-Request": "true", "HX-Target": "hm-ss-drawer"})
	if frag.Code != http.StatusOK || !strings.HasPrefix(frag.Body.String(), `<div data-hm-ss-id="`) || strings.Contains(frag.Body.String(), "<html") {
		t.Errorf("fragment: %d %.120s", frag.Code, frag.Body.String())
	}
	// Another form's submission is a 404 and is not touched.
	if rec := g.do("GET", base+"?s="+foreign.ID.String(), "owner-token", nil, nil); rec.Code != http.StatusNotFound {
		t.Errorf("foreign submission: %d, want 404", rec.Code)
	}
	if ff.subs[foreign.ID].Status != form.SubmissionStatusNew {
		t.Error("a foreign submission was marked read")
	}
	// Unknown or malformed ids show the tab without a drawer.
	for _, s := range []string{uuid.NewString(), "nope"} {
		rec := g.do("GET", base+"?s="+s, "owner-token", nil, nil)
		if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "data-hm-ss-autoopen") {
			t.Errorf("?s=%s: %d, want the tab without a drawer", s, rec.Code)
		}
	}
}

func TestSubmissionsTabWebhooksAreOwnerOnly(t *testing.T) {
	t.Parallel()

	g, ff, fm, base := submissionsTabRig(t, func(d *Deps) {
		d.Webhooks = &fakeWebhooks{subs: []webhook.ListedSubscription{{ID: "w1", Name: "Notify CRM", Events: []string{webhook.EventFormSubmitted}, Active: true}}}
	})
	sub := ff.addSub(fm, form.SubmissionStatusRead, time.Now(), map[string]any{"name": "Ada"})
	u := base + "?s=" + sub.ID.String()
	if owner := g.do("GET", u, "owner-token", nil, nil).Body.String(); !strings.Contains(owner, "Notify CRM") {
		t.Error("the owner's drawer lacks the webhook names")
	}
	editor := g.do("GET", u, "editor-token", nil, nil)
	if editor.Code != http.StatusOK || strings.Contains(editor.Body.String(), "Notify CRM") || strings.Contains(editor.Body.String(), "Webhooks") {
		t.Errorf("an editor's drawer carries webhooks: %d", editor.Code)
	}
}

// The header's Export CSV link is not part of the swapped results, so the live
// search must refresh it out of band: the page names it in the filterbar's
// out-of-band list, and the response carries a link for the current q and status.
func TestSubmissionsTabExportLinkFollowsSearchAndStatus(t *testing.T) {
	t.Parallel()

	g, ff, fm, base := submissionsTabRig(t)
	ff.addSub(fm, form.SubmissionStatusNew, time.Now(), map[string]any{"name": "Acme"})
	body := g.do("GET", base+"?q=acme&status=new", "owner-token", nil, map[string]string{"HX-Request": "true"}).Body.String()
	want := `href="/admin/forms/submissions/export.csv?form=` + fm.ID.String() + `&amp;status=new&amp;q=acme"`
	i := strings.Index(body, `id="ss-export"`)
	if i < 0 || !strings.Contains(body[max(0, i-200):i+200], want) {
		t.Errorf("response lacks the export link %q next to id=ss-export", want)
	}
	if !strings.Contains(body, `hx-select-oob="#ss-count,#ss-status:innerHTML,#ss-export"`) {
		t.Error("the filterbar does not refresh the export link out of band")
	}
}
