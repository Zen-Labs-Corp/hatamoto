package render

import (
	"html/template"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

const goldenCommentAction = "/-/comments/6f1c2a3e-0b4d-4c5e-8f70-1a2b3c4d5e6f"

// commentSingleView is the tpl-single fixture of TestGoldenHierarchyTemplates.
func commentSingleView() *PageView {
	v := hierarchyView()
	pub := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	v.EntityID, v.SchemaSlug, v.Kind = "e1", "post", KindSingle
	v.Entity = &EntityView{
		ID: "e1", Title: "Hello world", Slug: "hello", Schema: "post", URL: "/blog/hello",
		Excerpt: "A short intro.", Image: "/media/a.jpg", PublishedAt: pub,
	}
	v.Attrs = map[string]any{"content": "<p>Only content here.</p>"}
	return v
}

// commentThreadFixture has three top-level comments: one with a two-deep reply
// chain and one written by the site author.
func commentThreadFixture() *CommentThread {
	at := time.Date(2031, 5, 30, 8, 0, 0, 0, time.UTC)
	item := func(id, author, body string, depth int, replies ...CommentItem) CommentItem {
		return CommentItem{
			ID: id, Author: author, Body: template.HTML("<p>" + body + "</p>"), CreatedAt: at,
			Depth: depth, Replies: replies, ReplyURL: "/blog/hello?replyto=" + id + "#respond",
		}
	}
	chain := item("c1", "Grace", "Top level.", 1,
		item("c2", "Linus", "First reply.", 2,
			item("c3", "Ken", "Second reply.", 3)))
	site := item("c4", "Site team", "Thanks for reading.", 1)
	site.IsSiteAuthor = true
	// A dead placeholder (no author, no body) keeps its reply; a revealed dead item shows both.
	dead := CommentItem{ID: "c6", CreatedAt: at, Depth: 1, Dead: true,
		Replies: []CommentItem{item("c7", "Ada", "Reply under the dead one.", 2)}}
	revealed := item("c8", "Barbara", "Shown with show dead.", 1)
	revealed.Dead = true
	return &CommentThread{
		Items: []CommentItem{chain, site, item("c5", "Margaret", "Another view.", 1), dead, revealed},
		Total: 8, Page: 1, Pages: 1, Newest: true,
	}
}

func commentOpenView() *PageView {
	v := commentSingleView()
	form := CommentFormPlaceholders
	form.Action, form.MaxBody = goldenCommentAction, 5000
	v.Comments, v.CommentsOpen, v.CommentForm = commentThreadFixture(), true, &form
	return v
}

func TestGoldenComments(t *testing.T) {
	nowFunc = func() time.Time { return time.Date(2031, 6, 1, 0, 0, 0, 0, time.UTC) }
	defer func() { nowFunc = time.Now }()
	e := hierarchyEngine(t)

	t.Run("comments-thread", func(t *testing.T) {
		compareGolden(t, "comments-thread", mainInner(t, renderToString(t, e, commentOpenView())))
	})
	t.Run("comments-closed", func(t *testing.T) {
		v := commentOpenView()
		v.CommentsOpen, v.CommentForm = false, nil
		compareGolden(t, "comments-closed", mainInner(t, renderToString(t, e, v)))
	})
	t.Run("comments-confirm", func(t *testing.T) {
		v := hierarchyView()
		v.CommentConfirm = &CommentConfirmView{EntityTitle: "Hello", Action: "/-/comments/confirm/x"}
		var b strings.Builder
		if err := e.RenderCommentConfirm(&b, v, 200); err != nil {
			t.Fatal(err)
		}
		compareGolden(t, "comments-confirm", mainInner(t, b.String()))
	})
}

var reNoWebsiteField = regexp.MustCompile(`(?i)website|homepage|name="url"`)

func TestCommentsOpenMarkup(t *testing.T) {
	e := hierarchyEngine(t)
	out := renderToString(t, e, commentOpenView())
	for _, want := range []string{CommentFormMarker, "__hmcf_body__", `id="hm-comments"`, `for="__hmcf_name__"`} {
		if !strings.Contains(out, want) {
			t.Errorf("open page lacks %q", want)
		}
	}
	// Only the comments section: the page chrome may say "WebSite" in its JSON-LD.
	if m := reNoWebsiteField.FindString(out[strings.Index(out, `id="hm-comments"`):]); m != "" {
		t.Errorf("open page has a website field: %q", m)
	}
}

// With comments off the page is byte-identical to the one that predates them.
func TestCommentsOffPathUnchanged(t *testing.T) {
	nowFunc = func() time.Time { return time.Date(2031, 6, 1, 0, 0, 0, 0, time.UTC) }
	defer func() { nowFunc = time.Now }()
	e := hierarchyEngine(t)
	v := hierarchyView()
	pub := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	ada := &AuthorView{Name: "Ada Lovelace", Slug: "ada-lovelace", URL: "/author/ada-lovelace", Avatar: "/media/ada.png",
		JobTitle: "Mathematician", SameAs: []string{"https://example.test/ada", "https://linkedin.example/ada"},
		Links: []AuthorLink{{"Website", "https://example.test/ada"}, {"LinkedIn", "https://linkedin.example/ada"}}}
	v.EntityID, v.SchemaSlug, v.Kind = "e1", "post", KindSingle
	v.Entity = &EntityView{
		ID: "e1", Title: "Hello world", Slug: "hello", Schema: "post", URL: "/blog/hello",
		Excerpt: "A short intro.", Image: "/media/a.jpg", Author: ada, PublishedAt: pub,
		Terms: []TermView{{Kind: "category", Name: "News", URL: "/category/news"}, {Kind: "tag", Name: "Go", URL: "/tag/go"}},
	}
	v.Attrs = map[string]any{"content": `<p>Only <a href="/x">content</a> here.</p><figure class="wp-block-image alignwide"><img src="/media/a.jpg" alt="A"><figcaption>Caption</figcaption></figure>`}
	if v.Comments != nil {
		t.Fatal("fixture must have comments off")
	}
	want, err := os.ReadFile("testdata/golden/tpl-single.html")
	if err != nil {
		t.Fatal(err)
	}
	if got := mainInner(t, renderToString(t, e, v)); got != string(want) {
		t.Errorf("comments-off output differs from tpl-single.html:\n%s", got)
	}
}
