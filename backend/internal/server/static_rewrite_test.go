package server

import (
	"bytes"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/statichost"
)

const testEntityID = "6f1c2a3e-0b4d-4c5e-8f70-1a2b3c4d5e6f"

const staticRewriteDoc = `<!doctype html><html><head><script src="/theme/assets/hm-consent.js?v=1"></script>` +
	`<script type="application/ld+json">{"urlTemplate":"https://static.example.net/search?q={search_term_string}"}</script></head>` +
	`<body><header><a href="/h">header</a></header>` +
	`<form method="post" action="/forms/contact/submit"><button formaction="/forms/other/submit">go</button></form>` +
	`<form action="/search"></form><form action="/fr/search"></form><form action="/-/x"></form>` +
	`<a href="/about">a</a> <a class="x" href="/f/x">f</a> <a href="/search?q=a&amp;page=2">s</a>` +
	`<a href="//cdn.example.net/x">c</a> <a href="https://example.net/y">y</a> <abbr>z</abbr> <a>none</a>` +
	`<form class="hm-form" method="post" action="/-/comments/` + testEntityID + `" data-hm-cform hx-post="/-/comments/` + testEntityID + `">` +
	`<input name="hm_ct" value="__hmcf_tok__"><input name="__hmcf_body__"><input name="__hmcf_name__"><input name="__hmcf_email__">` +
	`<input name="__hmcf_hp__"><input name="__hmcf_extra__"></form></body></html>`

func newTestRewrite(export bool) staticRewrite {
	return staticRewrite{
		hs:     statichost.StaticHosting{Static: true, Live: testStaticLive, Site: testStaticSite},
		export: export,
		live:   func(p string) bool { return staticLivePath(p, "") },
	}
}

func TestStaticRewritePage(t *testing.T) {
	t.Parallel()
	in := []byte(staticRewriteDoc)
	orig := append([]byte(nil), in...)
	out := string(newTestRewrite(false).page(in))
	if !bytes.Equal(in, orig) {
		t.Fatal("input was modified")
	}
	for _, want := range []string{
		`action="` + testStaticLive + `/forms/contact/submit"`,
		`formaction="` + testStaticLive + `/forms/other/submit"`,
		`action="` + testStaticLive + `/search"`,
		`action="` + testStaticLive + `/fr/search"`,
		`action="` + testStaticLive + `/-/x"`,
		`<a href="` + testStaticSite + `/about">`,
		`<a href="` + testStaticSite + `/h">`,
		`<a class="x" href="` + testStaticLive + `/f/x">`,
		`href="` + testStaticLive + `/search?q=a&amp;page=2"`,
		`href="//cdn.example.net/x"`,
		`href="https://example.net/y"`,
		`"` + testStaticLive + `/search?q={search_term_string}"`,
		`<head><meta name="hm-live-origin" content="` + testStaticLive + `"><script src="` + testStaticLive + render.AssetPrefix + render.LiveScriptName + `" defer></script><script src="/theme/assets/hm-consent.js`,
		`<div data-hm-live-form="` + testStaticLive + commentFormLivePrefix + testEntityID + `">`,
		`<a href="` + testStaticLive + commentFormLivePrefix + testEntityID + `">Leave a comment</a>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\n%s", want, out)
		}
	}
	p := render.CommentFormPlaceholders
	for _, bad := range []string{
		render.CommentFormMarker, p.Token, p.Body, p.Name, p.Email, p.Honeypot, p.Extra, "hm_ct",
		testStaticSite + "/search?q=", `hx-post=`,
	} {
		if strings.Contains(out, bad) {
			t.Errorf("output still contains %q", bad)
		}
	}
}

func TestStaticRewriteExportKeepsRelativeLinks(t *testing.T) {
	t.Parallel()
	out := string(newTestRewrite(true).page([]byte(staticRewriteDoc)))
	if !strings.Contains(out, `<a href="/about">`) || !strings.Contains(out, `<a href="/h">`) {
		t.Errorf("export page lost its relative links:\n%s", out)
	}
	if !strings.Contains(out, `href="`+testStaticLive+`/f/x"`) {
		t.Errorf("live link not rewritten in export")
	}
}

func TestStaticRewriteNoCommentFormNoScript(t *testing.T) {
	t.Parallel()
	out := string(newTestRewrite(false).page([]byte(`<html><head></head><body><a href="/about">a</a></body></html>`)))
	if strings.Contains(out, render.LiveScriptName) {
		t.Errorf("loader script without a comment form: %s", out)
	}
	if !strings.Contains(out, `<head><meta name="hm-live-origin"`) {
		t.Errorf("meta missing: %s", out)
	}
}

func TestStaticRewriteNoHeadNoMeta(t *testing.T) {
	t.Parallel()
	out := string(newTestRewrite(false).page([]byte(`<header><a href="/f/x">f</a></header>`)))
	if strings.Contains(out, "hm-live-origin") {
		t.Errorf("meta inserted without <head>: %s", out)
	}
	if !strings.Contains(out, `href="`+testStaticLive+`/f/x"`) {
		t.Errorf("link not rewritten: %s", out)
	}
}

func TestStaticRewriteDropsUnreadableCommentForm(t *testing.T) {
	t.Parallel()
	out := string(newTestRewrite(false).page([]byte(`<head></head><form action="/-/comments/not-an-id" data-hm-cform><input value="__hmcf_tok__"></form>`)))
	if strings.Contains(out, "__hmcf_tok__") || strings.Contains(out, "data-hm-cform") || strings.Contains(out, render.LiveScriptName) {
		t.Errorf("unreadable form shipped: %s", out)
	}
}

// The default theme wraps the form in its own div#hm-comment-form. The loader
// placeholder must not repeat that id: a duplicate id is invalid HTML, and the
// "#hm-comment-form" anchors of reply links would be ambiguous.
func TestStaticRewriteCommentLoaderKeepsIDUnique(t *testing.T) {
	t.Parallel()
	doc := strings.Replace(staticRewriteDoc, `<form class="hm-form" method="post" action="/-/comments/`,
		`<div id="hm-comment-form"><form class="hm-form" method="post" action="/-/comments/`, 1)
	doc = strings.Replace(doc, `</form></body>`, `</form></div></body>`, 1)
	out := string(newTestRewrite(false).page([]byte(doc)))
	if n := strings.Count(out, `id="hm-comment-form"`); n != 1 {
		t.Errorf("id=\"hm-comment-form\" appears %d times, want 1\n%s", n, out)
	}
	want := `<div id="hm-comment-form"><div data-hm-live-form="` + testStaticLive + commentFormLivePrefix + testEntityID + `">`
	if !strings.Contains(out, want) {
		t.Errorf("placeholder is not inside the theme wrapper\n%s", out)
	}
}
