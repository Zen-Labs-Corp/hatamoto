package render

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/entity"
)

// B10 conversion identity: a page converted lazily (entity.LegacyPageDoc, the
// conversion every write applies) renders the same bytes as the page_sections
// it came from. The corpus is every page_sections array literal found in the
// repo's test sources and JSON fixtures, plus the odd shapes below, so a stored
// shape the conversion would change cannot first show up on a live page.

// sectionsCorpus extracts the page_sections arrays written in fixtures: JSON
// files as they are, Go test sources as raw or interpreted string literals.
func sectionsCorpus(t *testing.T) []string {
	t.Helper()
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		for _, cand := range []string{s, strings.NewReplacer(`\"`, `"`, `\\`, `\`).Replace(s)} {
			var arr []any
			if json.Unmarshal([]byte(cand), &arr) != nil || seen[cand] {
				continue
			}
			seen[cand] = true
			out = append(out, cand)
			return
		}
	}
	root := filepath.Join("..", "..", "..")
	for _, dir := range []string{"backend", "design"} {
		_ = filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(p, "_test.go") && !strings.HasSuffix(p, ".json") {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			src := string(b)
			for i := 0; ; {
				j := strings.Index(src[i:], "page_sections")
				if j < 0 {
					break
				}
				i += j + len("page_sections")
				rest := strings.TrimLeft(strings.TrimPrefix(src[i:], `\"`), `"`)
				rest = strings.TrimLeft(rest, " ")
				if !strings.HasPrefix(rest, ":") {
					continue
				}
				rest = strings.TrimLeft(rest[1:], " \n\t")
				if strings.HasPrefix(rest, "[") {
					if arr := balancedArray(rest); arr != "" {
						add(arr)
					}
				}
			}
			return nil
		})
	}
	return out
}

// balancedArray returns the bracket-balanced array at the start of s ("" when
// it does not close), skipping brackets inside strings.
func balancedArray(s string) string {
	depth, inStr := 0, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\':
			i++
		case c == '"':
			inStr = !inStr
		case inStr:
		case c == '[':
			depth++
		case c == ']':
			depth--
			if depth == 0 {
				return s[:i+1]
			}
		}
	}
	return ""
}

// oddSections are stored shapes the conversion has to survive.
var oddSections = []string{
	`[]`,
	`[{"id":"a","type":"content","content":{"body":"<p>x</p>"},"sort_order":0}]`,
	`[{"id":"a","type":"hero","content":{"headline":"H"},"sort_order":2},{"id":"b","type":"content","content":{"body":"<p>b</p>"},"sort_order":1}]`,
	`[{"id":"a","type":"content","content":{"body":"x"},"sort_order":1},{"id":"b","type":"content","content":{"body":"y"},"sort_order":1},{"id":"c","type":"content","content":{"body":"z"},"sort_order":0}]`,
	`[{"id":"a","type":"content","isVisible":false,"content":{"body":"hidden"}},{"id":"b","type":"content","isVisible":true,"content":{"body":"shown"}}]`,
	`[{"id":"dup","type":"content","content":{"body":"1"}},{"id":"dup","type":"content","content":{"body":"2"}}]`,
	`[{"type":"content","content":{"body":"no id"}},{"id":7,"type":"content","content":{"body":"numeric id"}},{"id":"","type":"content","content":{"body":"empty id"}}]`,
	`[{"id":"a","type":"content","sort_order":"3","content":{"body":"string order"}},{"id":"b","type":"content","sort_order":1.9,"content":{"body":"float order"}}]`,
	`[{"id":"a","type":"mystery","content":{"x":1}},{"id":"b","type":"html","content":{"body":"<div>raw</div>"}}]`,
	`[{"id":"a","type":"content","title":"T","anchor":"top","content":{"body":"x","price":9.90,"big":12345678901234567890}}]`,
	`[{"id":"a","type":"content"},{"id":"b","type":"content","content":null},"junk",null,3]`,
	`[{"id":"n1","type":"content","content":{"body":"x"}},{"id":"c-n1","type":"content","content":{"body":"clash with the wrapper id"}}]`,
}

func TestPageSectionsConversionIdentity(t *testing.T) {
	eng, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	corpus := sectionsCorpus(t)
	if len(corpus) < 15 {
		t.Fatalf("corpus too small (%d): the fixture extraction is broken", len(corpus))
	}
	refused := 0
	all := append(append([]string{}, oddSections...), corpus...)
	for i, secs := range all {
		attrs := json.RawMessage(`{"page_sections":` + secs + `}`)
		doc, ok := entity.LegacyPageDoc(attrs)
		if !ok {
			if !conversionRefusalExplained(builder.SectionsFromJSON(attrs, true)) {
				t.Errorf("#%d does not convert for an unexplained reason: %s", i, secs)
			}
			refused++
			continue
		}
		// What the renderer reads: the stored doc via builder.Parse.
		raw, _ := json.Marshal(doc)
		rdoc, err := builder.Parse(raw)
		if err != nil {
			t.Errorf("#%d converted doc does not parse: %v\n%s", i, err, secs)
			continue
		}
		view := func() *PageView {
			return &PageView{Kind: KindPage, Path: "/", EntityID: "e", Page: PageMeta{Name: "N", Title: "N"}}
		}
		a := view()
		for _, s := range builder.SectionsFromJSON(attrs, false) {
			a.Sections = append(a.Sections, Section{ID: s.ID, Type: s.Type, Title: s.Title, Content: s.Content, IsVisible: s.IsVisible, SortOrder: s.SortOrder})
		}
		b := view()
		b.Doc = rdoc
		var ba, bb bytes.Buffer
		if err := eng.RenderPage(&ba, a); err != nil {
			t.Errorf("#%d sections render: %v", i, err)
			continue
		}
		if err := eng.RenderPage(&bb, b); err != nil {
			t.Errorf("#%d doc render: %v", i, err)
			continue
		}
		if !bytes.Equal(ba.Bytes(), bb.Bytes()) {
			t.Errorf("#%d renders differently as a converted doc: %s", i, secs)
		}
	}
	t.Logf("%d shapes checked (%d from fixtures), %d refused (left unconverted)", len(all), len(corpus), refused)
}

// conversionRefusalExplained: the conversion refuses what it would have to
// repair (an id that is duplicated or that collides with a wrapper id: the
// public HTML carries section ids) and sections without a type (not valid
// component nodes). Such pages stay on page_sections and keep rendering.
func conversionRefusalExplained(ss []builder.Section) bool {
	ids := map[string]bool{}
	for _, s := range ss {
		if s.Type == "" || ids[s.ID] {
			return true
		}
		ids[s.ID] = true
	}
	for id := range ids {
		if ids["c-"+id] {
			return true
		}
	}
	return false
}
