package designcheck

import (
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"hatamoto.dev/core/web"
)

// htmx 4 drops implicit inheritance: an hx-target, hx-indicator or hx-include
// must sit on the element that makes the request, never on a wrapper. This
// reads the admin templates only; markup built in JS is not seen.
var (
	inheritKeys = []string{"hx-target", "hx-indicator", "hx-include"}
	hxVerb      = regexp.MustCompile(`^hx-(get|post|put|patch|delete)$`)
	attrmapCall = regexp.MustCompile(`\(attrmap [^)]*\)`)
	attrmapKey  = regexp.MustCompile(`"hx-(target|indicator|include)"`)
	attrmapVerb = regexp.MustCompile(`"hx-(get|post|put|patch|delete)"`)
	voidTags    = map[string]bool{"input": true, "br": true, "img": true, "hr": true, "meta": true, "link": true,
		"source": true, "wbr": true, "col": true, "area": true, "embed": true, "track": true}
)

// inheritAllow maps "file:line" to the reason a finding is accepted. Keep empty.
var inheritAllow = map[string]string{}

type inheritFrame struct {
	tag  string
	line int
	keys map[string]bool
}

func inheritFindings(name, src string) []string {
	var out []string
	// R3: an attrmap lands on one element, so a target/indicator/include
	// without a request verb in the same call is a wrapper.
	for _, m := range attrmapCall.FindAllStringIndex(src, -1) {
		call := src[m[0]:m[1]]
		if attrmapKey.MatchString(call) && !attrmapVerb.MatchString(call) {
			out = append(out, fmt.Sprintf("%s:%d: attrmap sets an hx-target/indicator/include without a request verb", name, lineOf(src, m[0])))
		}
	}

	// Blank template actions (same length, lines kept) and walk the tags with
	// the tokenizer: html.Parse would reorder duplicated {{if}}/{{else}} tags.
	blank := uiTemplateAct.ReplaceAllStringFunc(src, func(s string) string {
		return strings.Map(func(r rune) rune {
			if r == '\n' {
				return r
			}
			return ' '
		}, s)
	})
	var stack []inheritFrame
	z := html.NewTokenizer(strings.NewReader(blank))
	off := 0
	for {
		tt := z.Next()
		start := off
		off += len(z.Raw())
		if tt == html.ErrorToken {
			break
		}
		switch tt {
		case html.EndTagToken:
			tn, _ := z.TagName()
			name := string(tn)
			for i := len(stack) - 1; i >= 0; i-- {
				if stack[i].tag == name {
					stack = stack[:i]
					break
				}
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			tn, hasAttr := z.TagName()
			tag := string(tn)
			own := map[string]bool{}
			requester := false
			for hasAttr {
				var k []byte
				k, _, hasAttr = z.TagAttr()
				key := string(k)
				own[key] = true
				if hxVerb.MatchString(key) {
					requester = true
				}
			}
			line := lineOf(blank, start)
			if requester {
				for _, k := range inheritKeys {
					if own[k] {
						continue
					}
					for _, a := range stack {
						if a.keys[k] {
							out = append(out, fmt.Sprintf("%s:%d: <%s> requester inherits %s from <%s> at line %d", name, line, tag, k, a.tag, a.line))
							break
						}
					}
				}
			}
			if tt == html.StartTagToken && !voidTags[tag] {
				keys := map[string]bool{}
				for _, k := range inheritKeys {
					keys[k] = own[k]
				}
				stack = append(stack, inheritFrame{tag, line, keys})
			}
		}
	}
	return out
}

func TestHTMXInheritanceExplicit(t *testing.T) {
	t.Parallel()
	err := fs.WalkDir(web.Admin(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		b, err := fs.ReadFile(web.Admin(), p)
		if err != nil {
			return err
		}
		for _, f := range inheritFindings(p, string(b)) {
			key := strings.Join(strings.SplitN(f, ":", 3)[:2], ":")
			if _, ok := inheritAllow[key]; !ok {
				t.Error(f)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestHTMXInheritanceSelfTest(t *testing.T) {
	t.Parallel()
	bad := map[string]string{
		"wrapper":  `<div hx-target="#x"><button hx-get="/y">go</button></div>`,
		"nested":   `<form hx-get="/a" hx-target="#r" hx-indicator="#i"><input hx-get="/b" hx-target="#r" name="q"></form>`,
		"attrmap":  `{{template "hm/button" (dict "Label" "x" "Attrs" (attrmap "hx-target" "#x"))}}`,
		"template": `<div {{if .X}}hx-indicator="#i"{{end}}><a hx-get="/z" hx-target="#t">z</a></div>`,
	}
	for n, src := range bad {
		if len(inheritFindings(n, src)) == 0 {
			t.Errorf("%s not flagged", n)
		}
	}
	ok := `<div><button hx-get="/y" hx-target="#x" hx-indicator="#i">go</button></div>` +
		`{{template "hm/button" (dict "Attrs" (attrmap "hx-post" "/p" "hx-target" "#t"))}}`
	if f := inheritFindings("ok", ok); len(f) != 0 {
		t.Errorf("false positive %v", f)
	}
}
