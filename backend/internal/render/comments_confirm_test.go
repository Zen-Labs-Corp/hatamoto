package render

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

var scriptTagRe = regexp.MustCompile(`<script[^>]*>`)

func TestRenderCommentConfirm(t *testing.T) {
	e := newTestEngine(t, Options{})
	view := func(c *CommentConfirmView) *PageView {
		v := minimalView()
		v.Site.Nav = []NavItem{{Label: "Secret Pricing", Href: "/secret-pricing"}}
		v.CommentConfirm = c
		return v
	}
	render := func(c *CommentConfirmView) string {
		var b bytes.Buffer
		if err := e.RenderCommentConfirm(&b, view(c), 200); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	var acc bytes.Buffer
	av := view(nil)
	av.Access = &AccessView{}
	if err := e.RenderAccess(&acc, av, 200); err != nil {
		t.Fatal(err)
	}
	wantScripts := scriptTagRe.FindAllString(acc.String(), -1)

	ok := render(&CommentConfirmView{EntityTitle: "<b>Hi</b>", Action: "/-/comments/confirm/x"})
	for _, w := range []string{`action="/-/comments/confirm/x"`, `type="submit"`, "Confirm", "&lt;b&gt;Hi&lt;/b&gt;", `name="robots" content="noindex`} {
		if !strings.Contains(ok, w) {
			t.Errorf("confirm state lacks %q:\n%s", w, ok)
		}
	}
	if strings.Contains(ok, "<b>Hi</b>") || strings.Contains(ok, "Secret Pricing") {
		t.Error("title unescaped or nav leaked")
	}
	bad := render(&CommentConfirmView{Invalid: true})
	if !strings.Contains(bad, "Link expired") || strings.Contains(bad, "<form") {
		t.Errorf("invalid state wrong:\n%s", bad)
	}
	for name, out := range map[string]string{"confirm": ok, "invalid": bad} {
		got := scriptTagRe.FindAllString(out, -1)
		if strings.Join(got, "|") != strings.Join(wantScripts, "|") {
			t.Errorf("%s scripts %v differ from access page %v", name, got, wantScripts)
		}
	}
}
