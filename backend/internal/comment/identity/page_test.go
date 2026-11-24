package identity

import (
	"strings"
	"testing"
)

func render(t *testing.T, d PageData) string {
	t.Helper()
	var b strings.Builder
	if err := RenderPage(&b, d); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestRenderPage_ConfirmHasPostButtonOnly(t *testing.T) {
	t.Parallel()
	out := render(t, PageData{SiteName: "Site S", EntityTitle: "A title", Action: "/-/comments/confirm/tok", State: PageConfirm})
	for _, want := range []string{`method="post"`, `action="/-/comments/confirm/tok"`, "Site S", "A title", `name="robots" content="noindex"`, `no-referrer`, `lang="en"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
	for _, bad := range []string{"<script", "<input", "http://", "https://", "\u2013", "\u2014"} {
		if strings.Contains(out, bad) {
			t.Errorf("unexpected %q", bad)
		}
	}
}

func TestRenderPage_EscapesTitle(t *testing.T) {
	t.Parallel()
	out := render(t, PageData{SiteName: "S", EntityTitle: "<img src=x onerror=1>", Action: "/-/comments/confirm/t"})
	if strings.Contains(out, "<img") || !strings.Contains(out, "&lt;img") {
		t.Errorf("title not escaped: %s", out)
	}
}

func TestRenderPage_InvalidRevealsNothing(t *testing.T) {
	t.Parallel()
	out := render(t, PageData{SiteName: "Site S", EntityTitle: "A title", Action: "/-/comments/confirm/tok", State: PageInvalid})
	for _, bad := range []string{"A title", "/-/comments/confirm/tok", "<form", "<script", "\u2013", "\u2014"} {
		if strings.Contains(out, bad) {
			t.Errorf("invalid page contains %q", bad)
		}
	}
	if !strings.Contains(out, "expired or was already used") {
		t.Errorf("missing message: %s", out)
	}
}

func TestRenderPage_SignInStates(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		state PageState
		want  []string
		bad   []string
	}{
		{PageSignInForm, []string{`action="/-/comments/signin"`, `name="hm_ct" value="TOK"`, `type="email"`, "Send sign-in link"}, nil},
		{PageSignInSent, []string{"Check your email", "If this address can be used"}, []string{"<form"}},
		{PageSignedIn, []string{"signed in on this browser", `name="hm_ct" value="TOK"`, "Sign out"}, []string{`type="email"`}},
		{PageSignedOut, []string{"signed out on this browser"}, []string{"<form"}},
		{PageSignInLink, []string{`method="post"`, `action="/-/comments/signin"`, "Sign in</button>"}, []string{`type="email"`}},
		{PageSignInInvalid, []string{"expired or was already used"}, []string{"<form"}},
	} {
		out := render(t, PageData{SiteName: "Site S", Action: "/-/comments/signin", State: c.state, Token: "TOK"})
		for _, w := range append(c.want, `name="robots" content="noindex"`) {
			if !strings.Contains(out, w) {
				t.Errorf("state %d: missing %q in %s", c.state, w, out)
			}
		}
		for _, b := range append(c.bad, "<script", "\u2013", "\u2014") {
			if strings.Contains(out, b) {
				t.Errorf("state %d: unexpected %q", c.state, b)
			}
		}
	}
	out := render(t, PageData{State: PageSignInForm, Action: "/-/comments/signin", Error: "<b>x</b>"})
	if strings.Contains(out, "<b>x") || !strings.Contains(out, "&lt;b&gt;x") {
		t.Errorf("error not escaped: %s", out)
	}
}
