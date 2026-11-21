package server

import (
	"bytes"
	"html"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/statichost"
)

// staticRewrite turns a rendered page into the static-mode variant: every
// interactive target points at the live origin L and the comment form is
// replaced by a loader that fetches it from L. It is a pure byte rewrite, so it
// works for every theme and changes no theme contract.
type staticRewrite struct {
	hs     statichost.StaticHosting
	export bool                   // statichost.IsExport(ctx): keep S-relative links
	live   func(path string) bool // staticLivePath bound to the API prefix
}

// page returns body rewritten for static mode. The input is never modified (it
// may be the cached entry). Order matters: the comment form is replaced first,
// because the form's relative action is how its entity id is found.
func (x staticRewrite) page(body []byte) []byte {
	out, loader := x.commentLoader(body)
	for _, attr := range []string{` action="/`, ` formaction="/`, ` hx-post="/`} {
		out = rewriteAttr(out, attr, x.liveValue)
	}
	out = rewriteAnchors(out, func(v string) (string, bool) {
		if nv, ok := x.liveValue(v); ok {
			return nv, true
		}
		if !x.export {
			return html.EscapeString(x.hs.Site) + v, true
		}
		return "", false
	})
	// The search box of a static page must reach the live search.
	out = bytes.ReplaceAll(out,
		[]byte(`"`+x.hs.Site+`/search?q={search_term_string}"`),
		[]byte(`"`+x.hs.Live+`/search?q={search_term_string}"`))
	return x.insertHead(out, loader)
}

// liveValue prefixes a root-relative attribute value with L when its path is an
// island target only the live origin answers.
func (x staticRewrite) liveValue(v string) (string, bool) {
	if strings.HasPrefix(v, "//") {
		return "", false
	}
	p := v
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	if !x.live(p) {
		return "", false
	}
	return html.EscapeString(x.hs.Live) + v, true
}

// commentLoader replaces each comment form with a loader placeholder that holds
// no token: a static page is public and cached, so a per-view token must never
// be baked into it. It reports whether a placeholder was inserted. A form whose
// entity id cannot be read is dropped, because a placeholder must never ship.
func (x staticRewrite) commentLoader(body []byte) ([]byte, bool) {
	if !bytes.Contains(body, []byte(render.CommentFormMarker)) {
		return body, false
	}
	var (
		out    = body
		loader bool
	)
	for {
		start, end := commentFormSpan(out)
		if start < 0 {
			return out, loader
		}
		repl := ""
		if id, ok := commentSpanEntityID(out[start:end]); ok {
			url := html.EscapeString(x.hs.Live) + commentFormLivePrefix + id
			// The placeholder has no id: the theme's own wrapper around the form already
			// carries id="hm-comment-form", and a second one would be a duplicate id.
			repl = `<div data-hm-live-form="` + url + `"><p><a href="` + url + `">Leave a comment</a></p></div>`
			loader = true
		}
		next := make([]byte, 0, len(out)-(end-start)+len(repl))
		next = append(next, out[:start]...)
		next = append(next, repl...)
		out = append(next, out[end:]...)
	}
}

// commentSpanEntityID reads the entity id from the form's relative action, the
// same way commentSlot.fill does.
func commentSpanEntityID(span []byte) (string, bool) {
	i := bytes.Index(span, []byte(commentActionPrefix))
	if i < 0 {
		return "", false
	}
	rest := span[i+len(commentActionPrefix):]
	j := bytes.IndexByte(rest, '"')
	if j < 0 {
		return "", false
	}
	id, err := uuid.Parse(string(rest[:j]))
	if err != nil {
		return "", false
	}
	return id.String(), true
}

// insertHead puts the live-origin meta right after the opening <head> tag, so
// it comes before any script that reads it, and the comment loader script
// after it. Without a <head> nothing is inserted.
func (x staticRewrite) insertHead(body []byte, loader bool) []byte {
	i := openingTagEnd(body, "<head")
	if i < 0 {
		return body
	}
	ins := `<meta name="hm-live-origin" content="` + html.EscapeString(x.hs.Live) + `">`
	if loader {
		ins += `<script src="` + html.EscapeString(x.hs.Live) + render.AssetPrefix + render.LiveScriptName + `" defer></script>`
	}
	out := make([]byte, 0, len(body)+len(ins))
	out = append(out, body[:i]...)
	out = append(out, ins...)
	return append(out, body[i:]...)
}

// openingTagEnd returns the index just after the '>' of the first tag that
// starts with prefix (a name such as "<head", not "<header"); -1 when absent.
func openingTagEnd(body []byte, prefix string) int {
	for off := 0; ; {
		i := bytes.Index(body[off:], []byte(prefix))
		if i < 0 {
			return -1
		}
		at := off + i + len(prefix)
		if at < len(body) && (body[at] == '>' || body[at] == ' ' || body[at] == '\n' || body[at] == '\t') {
			gt := bytes.IndexByte(body[at:], '>')
			if gt < 0 {
				return -1
			}
			return at + gt + 1
		}
		off = at
	}
}

// rewriteAnchors applies fn to the href value of every <a> tag whose value is
// root-relative (starts with a single "/").
func rewriteAnchors(body []byte, fn func(v string) (string, bool)) []byte {
	var out []byte // allocated on the first change, so an unchanged page is returned as is
	last := 0
	for off := 0; ; {
		i := bytes.Index(body[off:], []byte("<a"))
		if i < 0 {
			break
		}
		start := off + i
		at := start + 2
		if at >= len(body) || (body[at] != ' ' && body[at] != '\n' && body[at] != '\t') {
			off = at
			continue
		}
		gt := bytes.IndexByte(body[at:], '>')
		if gt < 0 {
			break
		}
		end := at + gt
		tag := rewriteAttr(body[start:end], ` href="/`, fn)
		if !bytes.Equal(tag, body[start:end]) {
			out = append(out, body[last:start]...)
			out = append(out, tag...)
			last = end
		}
		off = end
	}
	if out == nil {
		return body
	}
	return append(out, body[last:]...)
}

// rewriteAttr applies fn to the value of every attribute introduced by attr
// (which includes the opening quote and the leading "/", e.g. ` action="/`).
// A value starting with "//" is skipped. The input is never modified.
func rewriteAttr(body []byte, attr string, fn func(v string) (string, bool)) []byte {
	var out []byte
	last := 0
	for off := 0; ; {
		i := bytes.Index(body[off:], []byte(attr))
		if i < 0 {
			break
		}
		vs := off + i + len(attr) - 1 // the "/" is part of the value
		q := bytes.IndexByte(body[vs:], '"')
		if q < 0 {
			break
		}
		ve := vs + q
		off = ve
		if ve > vs+1 && body[vs+1] == '/' {
			continue
		}
		nv, ok := fn(string(body[vs:ve]))
		if !ok {
			continue
		}
		out = append(out, body[last:vs]...)
		out = append(out, nv...)
		last = ve
	}
	if out == nil {
		return body
	}
	return append(out, body[last:]...)
}
