package admin

import (
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
)

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
