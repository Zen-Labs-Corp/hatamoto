package render

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAgoBoundaries(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	nowFunc = func() time.Time { return now }
	defer func() { nowFunc = time.Now }()
	for _, c := range []struct {
		age  time.Duration
		want Ago
	}{
		{-time.Hour, Ago{Unit: "now"}},
		{59 * time.Second, Ago{Unit: "now"}},
		{time.Minute, Ago{"min", 1}},
		{59 * time.Minute, Ago{"min", 59}},
		{time.Hour, Ago{"hour", 1}},
		{23 * time.Hour, Ago{"hour", 23}},
		{24 * time.Hour, Ago{"day", 1}},
		{30 * 24 * time.Hour, Ago{"day", 30}},
		{31 * 24 * time.Hour, Ago{Unit: "date"}},
	} {
		if got := ago(now.Add(-c.age)); got != c.want {
			t.Errorf("ago(-%s) = %+v, want %+v", c.age, got, c.want)
		}
	}
}

func TestCommentFieldsZeroValueSafe(t *testing.T) {
	var v PageView
	if v.Comments != nil || v.CommentForm != nil || v.CommentsOpen || v.CommentCount != 0 {
		t.Fatal("comment fields must default to off")
	}
}

func TestRenderCommentsFragmentHasForm(t *testing.T) {
	e := newTestEngine(t, Options{})
	v := minimalView()
	p := CommentFormPlaceholders
	p.Action = "/-/comments/e1"
	v.Comments = &CommentThread{}
	v.CommentForm = &p
	var b bytes.Buffer
	item := &CommentItem{ID: "c1", Author: "Ana", Body: "<p>Hi</p>", CreatedAt: time.Now()}
	if err := e.RenderCommentsFragment(&b, v, item); err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{CommentFormMarker, `hx-post="/-/comments/e1"`, `name="__hmcf_tok__"`, `afterbegin:#hm-comments-list`, `id="comment-c1"`} {
		if w == `name="__hmcf_tok__"` {
			w = `value="__hmcf_tok__"`
		}
		if !strings.Contains(b.String(), w) {
			t.Errorf("fragment lacks %q:\n%s", w, b.String())
		}
	}
	if strings.Contains(b.String(), "website") {
		t.Error("fragment carries a website field")
	}
}

func TestRenderCommentsFragmentEchoSlots(t *testing.T) {
	e := newTestEngine(t, Options{})
	render := func(withValues bool) string {
		v := minimalView()
		p := CommentFormPlaceholders
		p.Action = "/-/comments/e1"
		if withValues {
			p.BodyValue = CommentValuePlaceholders.BodyValue
			p.NameValue = CommentValuePlaceholders.NameValue
			p.EmailValue = CommentValuePlaceholders.EmailValue
			p.Notify = true
		}
		v.Comments = &CommentThread{}
		v.CommentForm = &p
		var b bytes.Buffer
		if err := e.RenderCommentsFragment(&b, v, nil); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	out := render(true)
	for _, w := range []string{`value="__hmcf_vname__"`, `value="__hmcf_vemail__"`, `>__hmcf_vbody__</textarea>`, ` checked>`} {
		if !strings.Contains(out, w) {
			t.Errorf("echo render lacks %q:\n%s", w, out)
		}
	}
	out = render(false)
	if strings.Contains(out, `maxlength="80" value=`) || strings.Contains(out, `maxlength="254" value=`) {
		t.Error("empty render carries value attributes")
	}
	if !strings.Contains(out, `required></textarea>`) || strings.Contains(out, "checked") {
		t.Errorf("empty render not clean:\n%s", out)
	}
}

func TestDefaultThemeCommentsOffEmitsNothing(t *testing.T) {
	e := newTestEngine(t, Options{})
	v := minimalView()
	v.Kind = KindSingle
	if strings.Contains(renderToString(t, e, v), "hm-comments") {
		t.Error("comments markup with Comments == nil")
	}
}

func TestDefaultThemeRendersThread(t *testing.T) {
	e := newTestEngine(t, Options{})
	v := minimalView()
	v.Kind = KindSingle
	v.Entity = &EntityView{ID: "e1", Title: "Hello", Schema: "post"}
	f := CommentFormPlaceholders
	f.Action = "/-/comments/e1"
	child := CommentItem{ID: "c2", Author: "Owner", IsSiteAuthor: true, Body: "<p>Thanks</p>", CreatedAt: time.Now().Add(-26 * time.Hour)}
	v.Comments = &CommentThread{Total: 2, Items: []CommentItem{{ID: "c1", Author: "Ana", Body: "<p>Hi</p>", CreatedAt: time.Now().Add(-5 * time.Hour), ReplyURL: "?creply=c1#hm-comment-form", Replies: []CommentItem{child}}}, NextURL: "?cpage=2#hm-comments", Page: 1, Pages: 2,
		PageLinks: []CommentPageLink{{Page: 1, URL: "#hm-comments", Current: true}, {Page: 2, URL: "?cpage=2#hm-comments"}}}
	v.CommentForm = &f
	out := renderToString(t, e, v)
	for _, w := range []string{`id="hm-comments"`, "2 comments", "5 hours ago", "1 day ago", "Thanks", `?cpage=2#hm-comments`, `<span aria-current="page">1</span>`, `<a href="?cpage=2#hm-comments" rel="next">2</a>`, `hm-comments__list--nested`, `href="/theme/assets/comments.css"`} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q", w)
		}
	}
}

func TestCommentsStylesheetServedOutsideBundle(t *testing.T) {
	th, err := LoadTheme(Chain{DefaultFS()})
	if err != nil {
		t.Fatal(err)
	}
	css, _, _ := buildBundle(th)
	if bytes.Contains(css, []byte(".hm-comments")) {
		t.Error("comments.css leaked into the global bundle")
	}
	rec := httptest.NewRecorder()
	th.AssetHandler().ServeHTTP(rec, httptest.NewRequest("GET", AssetPrefix+"comments.css", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), ".hm-comments") {
		t.Errorf("comments.css not served: %d", rec.Code)
	}
}
