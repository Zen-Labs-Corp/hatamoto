package admin

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
)

// The a11y tests below cover both pages (TestEveryPageTemplateIsExercised).
func init() {
	areaPages["commentssettings"] = []string{"settings/comments", "settings/comments-enable"}
}

const csetURL = "/admin/settings/comments"

func csetGet(t *testing.T, p *perfRig, token string) string {
	t.Helper()
	rec := p.do("GET", csetURL, token, nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET = %d", rec.Code)
	}
	body := rec.Body.String()
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	return body
}

func TestCommentSettingsOffShowsCallout(t *testing.T) {
	t.Parallel()

	p := newCommentsRig(t, &fakeComments{})
	body := csetGet(t, p, "owner-token")
	for _, want := range []string{csetCallout, csetSecondWarning, csetTick, `data-hm-open="comments-enable"`, `id="comments-enable"`, `id="comments-warning"`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	i := strings.Index(body, ">Turn on<")
	if i < 0 {
		t.Fatal("no Turn on button")
	}
	open := body[strings.LastIndex(body[:i], "<button"):i]
	if strings.Contains(open, " disabled") {
		t.Errorf("Turn on button is disabled in server HTML: %s", open)
	}
}

func TestCommentSettingsEditorReadOnly(t *testing.T) {
	t.Parallel()

	p := newCommentsRig(t, &fakeComments{})
	body := csetGet(t, p, "editor-token")
	for _, want := range []string{"Only an owner can turn comments on or off."} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	i := strings.Index(body, `id="comments-enabled"`)
	if i < 0 {
		t.Fatal("no switch")
	}
	tag := body[strings.LastIndex(body[:i], "<input"):]
	tag = tag[:strings.Index(tag, ">")]
	if !strings.Contains(tag, " disabled") {
		t.Errorf("switch not disabled: %s", tag)
	}
	for _, bad := range []string{`id="comments-enable"`, ">Save<", "comments-enable-form"} {
		if strings.Contains(body, bad) {
			t.Errorf("editor page has %q", bad)
		}
	}
}

func TestCommentSettingsOnHidesCallout(t *testing.T) {
	t.Parallel()

	p := newCommentsRig(t, &fakeComments{settings: CommentSettings{Enabled: true}})
	body := csetGet(t, p, "owner-token")
	for _, bad := range []string{csetCallout, `id="comments-enable"`} {
		if strings.Contains(body, bad) {
			t.Errorf("page has %q", bad)
		}
	}
	if !strings.Contains(body, `id="comments-enabled"`) || !strings.Contains(body, " checked") {
		t.Error("switch not shown on")
	}
}

func csetPost(p *perfRig, path, token string, form url.Values, hdr map[string]string) *httptest.ResponseRecorder {
	return p.do("POST", csetURL+path, token, form, hdr)
}

// The handler re-checks the owner role itself, whatever the mount group does.
func TestCommentEnablePageHandlerRefusesEditor(t *testing.T) {
	t.Parallel()

	p := newCommentsRig(t, &fakeComments{})
	r := httptest.NewRequest("GET", csetURL+"/enable", nil)
	r = r.WithContext(context.WithValue(r.Context(), userKey, &CurrentUser{ID: "u", Role: "editor"}))
	w := httptest.NewRecorder()
	p.a.handleCommentEnablePage(w, r)
	if w.Code != 403 {
		t.Errorf("editor = %d, want 403", w.Code)
	}
}

func TestCommentEnablePage(t *testing.T) {
	t.Parallel()

	p := newCommentsRig(t, &fakeComments{})
	rec := p.do("GET", csetURL+"/enable", "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET = %d", rec.Code)
	}
	body := rec.Body.String()
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	for _, want := range []string{csetSecondWarning, csetTick} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	i := strings.Index(body, ">Turn on<")
	if i < 0 {
		t.Fatal("no Turn on button")
	}
	if strings.Contains(body[strings.LastIndex(body[:i], "<button"):i], " disabled") {
		t.Error("Turn on button is disabled in server HTML")
	}
}

func TestCommentEnableNeedsTick(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{}
	p := newCommentsRig(t, svc)
	rec := csetPost(p, "/enable", "owner-token", url.Values{"understand": {"0"}}, nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "Tick the box to confirm you understand.") {
		t.Fatalf("POST = %d", rec.Code)
	}
	if len(svc.calls) != 0 || len(p.events) != 0 {
		t.Errorf("calls %v events %v", svc.calls, p.events)
	}
}

func TestCommentEnable(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{}
	p := newCommentsRig(t, svc)
	rec := csetPost(p, "/enable", "owner-token", url.Values{"understand": {"1"}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != csetURL {
		t.Fatalf("POST = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if len(svc.calls) != 1 || svc.calls[0] != "set-enabled" || !svc.settings.Enabled {
		t.Errorf("calls %v enabled %v", svc.calls, svc.settings.Enabled)
	}
	if len(p.events) != 1 || p.events[0].Action != "comments.enable" || p.events[0].Detail["old"] != false || p.events[0].Detail["new"] != true || len(p.events[0].Detail) != 2 {
		t.Fatalf("events %+v", p.events)
	}
	if body := p.followFlash(rec.Result().Cookies()); !strings.Contains(body, "Comments are on. New comments wait in the moderation queue.") {
		t.Error("no success flash")
	}
}

func TestCommentEnableHTMX(t *testing.T) {
	t.Parallel()

	p := newCommentsRig(t, &fakeComments{})
	rec := csetPost(p, "/enable", "owner-token", url.Values{"understand": {"1"}}, map[string]string{"HX-Request": "true"})
	if rec.Header().Get("HX-Redirect") != csetURL {
		t.Errorf("HX-Redirect = %q (status %d)", rec.Header().Get("HX-Redirect"), rec.Code)
	}
}

func TestCommentSwitchOnRedirectsToConfirm(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{}
	p := newCommentsRig(t, svc)
	rec := csetPost(p, "/switch", "owner-token", url.Values{"enabled": {"true"}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != csetURL+"/enable" {
		t.Fatalf("POST = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if len(svc.calls) != 0 || len(p.events) != 0 {
		t.Errorf("calls %v events %v", svc.calls, p.events)
	}
}

func TestCommentSwitchOff(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{settings: CommentSettings{Enabled: true}}
	p := newCommentsRig(t, svc)
	rec := csetPost(p, "/switch", "owner-token", url.Values{"enabled": {"off"}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != csetURL {
		t.Fatalf("POST = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if len(svc.calls) != 1 || svc.calls[0] != "set-enabled" || svc.settings.Enabled {
		t.Errorf("calls %v enabled %v", svc.calls, svc.settings.Enabled)
	}
	if len(p.events) != 1 || p.events[0].Action != "comments.disable" || p.events[0].Detail["old"] != true || p.events[0].Detail["new"] != false {
		t.Fatalf("events %+v", p.events)
	}
	if body := p.followFlash(rec.Result().Cookies()); !strings.Contains(body, "Comments are off. Nothing new can be posted.") {
		t.Error("no success flash")
	}
}

func TestCommentEnableSaveError(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{err: errors.New("store down")}
	p := newCommentsRig(t, svc)
	rec := csetPost(p, "/enable", "owner-token", url.Values{"understand": {"1"}}, nil)
	if rec.Code != 303 {
		t.Fatalf("POST = %d", rec.Code)
	}
	if len(p.events) != 0 {
		t.Errorf("events %+v", p.events)
	}
	if body := p.followFlash(rec.Result().Cookies()); !strings.Contains(body, "Comments could not be turned on.") {
		t.Error("no error flash")
	}
}

func TestCommentEnableEditor403(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{}
	p := newCommentsRig(t, svc)
	for _, path := range []string{"/enable", "/switch"} {
		if rec := csetPost(p, path, "editor-token", url.Values{"understand": {"1"}, "enabled": {"true"}}, nil); rec.Code != 403 {
			t.Errorf("editor POST %s = %d", path, rec.Code)
		}
	}
	if len(svc.calls) != 0 || len(p.events) != 0 {
		t.Errorf("calls %v events %v", svc.calls, p.events)
	}
}

func csetValidForm() url.Values {
	return url.Values{
		"known_min_approved": {"3"}, "known_min_age_days": {"7"}, "trusted_min_approved": {"15"}, "trusted_min_age_days": {"30"},
		"auto_close_days": {"30"}, "max_depth": {"5"}, "page_size": {"50"}, "sort": {"newest"},
		"slow_mode_seconds": {"120"}, "cookie_days": {"90"}, "purge_days": {"30"},
	}
}

func csetDefaults() CommentSettings {
	return CommentSettings{
		Enabled: true, KnownMinApproved: 3, KnownMinAgeDays: 7, TrustedMinApproved: 15, TrustedMinAgeDays: 30,
		AutoCloseDays: 30, MaxDepth: 5, PageSize: 50, Sort: "newest", SlowModeSeconds: 120, CookieDays: 90, PurgeDays: 30,
	}
}

func TestCommentSettingsShowsStoredValues(t *testing.T) {
	t.Parallel()

	p := newCommentsRig(t, &fakeComments{settings: csetDefaults()})
	body := csetGet(t, p, "owner-token")
	for _, want := range []string{"Trust levels", "Threads", "Limits", `id="comments-max-depth"`, `name="max_depth" value="5"`, `name="known_min_approved" value="3"`, `name="cookie_days" value="90"`, `name="trusted_min_age_days" value="30"`, `<option value="newest" selected>`, ">Save<"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func TestCommentSettingsSave(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{settings: csetDefaults()}
	p := newCommentsRig(t, svc)
	form := csetValidForm()
	form.Set("max_depth", "7")
	form.Set("sort", "oldest")
	form.Set("captcha", "true")
	form.Set("enabled", "off") // the form can never change Enabled
	rec := csetPost(p, "", "owner-token", form, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != csetURL {
		t.Fatalf("POST = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if len(svc.calls) != 1 || svc.calls[0] != "save-settings" {
		t.Fatalf("calls %v", svc.calls)
	}
	if s := svc.settings; !s.Enabled || s.MaxDepth != 7 || s.Sort != "oldest" || !s.Captcha {
		t.Errorf("saved %+v", s)
	}
	if len(p.events) != 1 || p.events[0].Action != "comments.settings" {
		t.Fatalf("events %+v", p.events)
	}
	changed, _ := p.events[0].Detail["changed"].([]string)
	if strings.Join(changed, ",") != "max_depth,sort,captcha" || len(p.events[0].Detail) != 1 {
		t.Errorf("detail %+v", p.events[0].Detail)
	}
	if body := p.followFlash(rec.Result().Cookies()); !strings.Contains(body, "Comment settings saved.") {
		t.Error("no success flash")
	}
}

func TestCommentSettingsValidation(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]struct {
		field, value, want string
	}{
		"depth too deep":    {"max_depth", "11", "Enter a whole number from 1 to 10."},
		"page size zero":    {"page_size", "0", "Enter a whole number from 1 to 200."},
		"not a number":      {"purge_days", "soon", "Enter a whole number from 1 to 3650."},
		"trusted < known":   {"trusted_min_approved", "2", "Use at least 3, the number for known commenters."},
		"trusted age short": {"trusted_min_age_days", "6", "Use at least 7, the number of days for known commenters."},
		"bad sort":          {"sort", "random", "Choose newest first or oldest first."},
	} {
		svc := &fakeComments{settings: csetDefaults()}
		p := newCommentsRig(t, svc)
		form := csetValidForm()
		form.Set(c.field, c.value)
		rec := csetPost(p, "", "owner-token", form, nil)
		if rec.Code != 422 || !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("%s: %d, want message %q", name, rec.Code, c.want)
		}
		if len(svc.calls) != 0 || len(p.events) != 0 {
			t.Errorf("%s: invalid form saved", name)
		}
	}
	svc := &fakeComments{settings: csetDefaults()}
	p := newCommentsRig(t, svc)
	form := csetValidForm()
	form.Set("max_depth", "7")
	if rec := csetPost(p, "", "owner-token", form, nil); rec.Code != 303 || svc.settings.MaxDepth != 7 {
		t.Errorf("depth 7 = %d", rec.Code)
	}
}

// csetRefusing is a service whose store refuses the settings it is given.
type csetRefusing struct{ *fakeComments }

func (csetRefusing) SaveSettings(context.Context, CommentSettings, string) error {
	return &CommentFieldError{Field: "max_depth", Message: "Depth is capped by the store."}
}

func TestCommentSettingsStoreFieldError(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{settings: csetDefaults()}
	p := newPerfRig(t, nil)
	p.a.deps.Comments = csetRefusing{svc}
	rec := csetPost(p, "", "owner-token", csetValidForm(), nil)
	body := rec.Body.String()
	if rec.Code != 422 || !strings.Contains(body, "Depth is capped by the store.") || !strings.Contains(body, `id="comments-max-depth-error"`) {
		t.Fatalf("POST = %d", rec.Code)
	}
	if len(p.events) != 0 {
		t.Errorf("events %+v", p.events)
	}
}

func TestCommentSettingsHiddenWhenOff(t *testing.T) {
	t.Parallel()

	p := newCommentsRig(t, &fakeComments{})
	body := csetGet(t, p, "owner-token")
	for _, bad := range []string{"Trust levels", "Threads", "max_depth"} {
		if strings.Contains(body, bad) {
			t.Errorf("page has %q while comments are off", bad)
		}
	}
}

func TestCommentSettingsEditorDisabled(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{settings: csetDefaults()}
	p := newCommentsRig(t, svc)
	body := csetGet(t, p, "editor-token")
	if !strings.Contains(body, "Trust levels") || !strings.Contains(body, "<fieldset class=\"hm-field__group\" disabled>") {
		t.Error("editor does not see the disabled settings")
	}
	if strings.Contains(body, ">Save<") {
		t.Error("editor sees Save")
	}
	if rec := csetPost(p, "", "editor-token", csetValidForm(), nil); rec.Code != 403 {
		t.Errorf("editor POST = %d", rec.Code)
	}
	if len(svc.calls) != 0 || len(p.events) != 0 {
		t.Errorf("calls %v events %v", svc.calls, p.events)
	}
}
