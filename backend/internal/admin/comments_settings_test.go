package admin

import (
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
