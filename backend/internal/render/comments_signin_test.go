package render

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestCommentSignInStates(t *testing.T) {
	e := newTestEngine(t, Options{})
	render := func(s *CommentSignInView) string {
		v := minimalView()
		v.Site.Nav = []NavItem{{Label: "Secret Pricing", Href: "/secret-pricing"}}
		v.CommentSignIn = s
		var b bytes.Buffer
		if err := e.RenderCommentSignIn(&b, v, 200); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	var acc bytes.Buffer
	av := minimalView()
	av.Access = &AccessView{}
	if err := e.RenderAccess(&acc, av, 200); err != nil {
		t.Fatal(err)
	}
	wantScripts := strings.Join(scriptTagRe.FindAllString(acc.String(), -1), "|")

	tests := []struct {
		name string
		view *CommentSignInView
		want []string
	}{
		{"form", &CommentSignInView{State: CommentSignInForm, Action: "/-/comments/signin", Token: "tok1", EmailName: "em", Email: "a@x.test"},
			[]string{"Enter your email address", `action="/-/comments/signin"`, `name="hm_ct" value="tok1"`, `name="em"`, `value="a@x.test"`, "Send sign-in link"}},
		{"form error", &CommentSignInView{State: CommentSignInForm, Action: "/-/comments/signin", EmailName: "em", Error: "email"},
			[]string{"Please enter a valid email address."}},
		{"sent", &CommentSignInView{State: CommentSignInSent}, []string{"a sign-in link is on its way"}},
		{"in", &CommentSignInView{State: CommentSignInIn, Action: "/-/comments/signout", Token: "tok2"},
			[]string{"You are signed in on this browser.", `action="/-/comments/signout"`, `value="tok2"`, "Sign out"}},
		{"out", &CommentSignInView{State: CommentSignInOut}, []string{"You are signed out on this browser."}},
		{"link", &CommentSignInView{State: CommentSignInLink, Action: "/-/comments/signin/abc"},
			[]string{`action="/-/comments/signin/abc"`, "Press the button to sign this browser in:"}},
		{"invalid", &CommentSignInView{State: CommentSignInInvalid}, []string{"Link expired", "expired or was already used"}},
		{"nil", nil, []string{"Link expired"}},
	}
	for _, tt := range tests {
		out := render(tt.view)
		for _, w := range append(tt.want, `name="robots" content="noindex`) {
			if !strings.Contains(out, w) {
				t.Errorf("%s lacks %q:\n%s", tt.name, w, out)
			}
		}
		if strings.Contains(out, "Secret Pricing") {
			t.Errorf("%s leaked nav", tt.name)
		}
		if got := strings.Join(scriptTagRe.FindAllString(out, -1), "|"); got != wantScripts {
			t.Errorf("%s scripts %q differ from access page %q", tt.name, got, wantScripts)
		}
	}
}

func commentFormOut(t *testing.T, thread *CommentThread, form CommentForm) string {
	t.Helper()
	e := newTestEngine(t, Options{})
	v := minimalView()
	form.Action = "/-/comments/e1"
	v.Comments, v.CommentForm = thread, &form
	var b bytes.Buffer
	item := &CommentItem{ID: "c1", Author: "Ana", Body: "<p>Hi</p>", CreatedAt: time.Now()}
	if err := e.RenderCommentsFragment(&b, v, item); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestCommentFormErrorCodes(t *testing.T) {
	form := CommentFormPlaceholders
	out := commentFormOut(t, &CommentThread{Error: "draft", ErrorText: "<b>x</b>"}, form)
	if !strings.Contains(out, "&lt;b&gt;x&lt;/b&gt;") || strings.Contains(out, "<b>x</b>") || strings.Contains(out, "Sign-in name unavailable") {
		t.Errorf("draft with text wrong:\n%s", out)
	}
	if out := commentFormOut(t, &CommentThread{Error: "draft"}, form); !strings.Contains(out, "Sign-in name unavailable, try again.") {
		t.Errorf("draft without text lacks generic text:\n%s", out)
	}
	out = commentFormOut(t, &CommentThread{Error: "sign_in"}, form)
	if !strings.Contains(out, `role="alert">You need to sign in to comment.`) || strings.Contains(out, "Comments are closed.") {
		t.Errorf("sign_in wrong:\n%s", out)
	}
}

func TestCommentFormLocked(t *testing.T) {
	form := CommentFormPlaceholders
	form.Locked, form.SignInURL = true, "/-/comments/signin"
	out := commentFormOut(t, &CommentThread{}, form)
	p := CommentLockedPlaceholders
	for _, w := range []string{p.Named, p.SignedIn, p.SignedOut, p.BodyOff, p.Who} {
		want := 1
		if w == p.BodyOff {
			want = 2 // the textarea and the submit button
		}
		if n := strings.Count(out, w); n != want {
			t.Errorf("%s appears %d times:\n%s", w, n, out)
		}
	}
	span := out[strings.Index(out, CommentFormMarker):]
	if !strings.Contains(span, "Posting as <strong>"+p.Who+"</strong>") || strings.Index(span, p.Named) > strings.Index(span, p.Who) || strings.Index(span, p.Who) > strings.Index(span, p.SignedIn) {
		t.Errorf("who is not inside the named line:\n%s", out)
	}
	if !strings.Contains(out, `type="submit" `+p.BodyOff+`>`) {
		t.Errorf("submit button does not carry the body switch:\n%s", out)
	}
	if !strings.Contains(out, `href="/-/comments/signin"`) {
		t.Errorf("sign-in link missing:\n%s", out)
	}
	for _, w := range []string{"__hmcf_name__", "__hmcf_email__"} {
		if strings.Contains(out, w) {
			t.Errorf("locked form still has %s", w)
		}
	}
	for _, w := range []string{`value="__hmcf_tok__"`, `name="__hmcf_body__"`, `name="__hmcf_hp__"`} {
		if !strings.Contains(out, w) {
			t.Errorf("locked form lacks %s", w)
		}
	}
	// The unlocked form carries none of the locked slots.
	un := commentFormOut(t, &CommentThread{}, CommentFormPlaceholders)
	hasLocked := false
	for _, w := range []string{p.Named, p.SignedIn, p.SignedOut, p.BodyOff, p.Who} {
		hasLocked = hasLocked || strings.Contains(un, w)
	}
	if hasLocked || !strings.Contains(un, "__hmcf_name__") {
		t.Errorf("unlocked form changed:\n%s", un)
	}
}
