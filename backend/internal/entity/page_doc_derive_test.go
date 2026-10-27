package entity

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/builder"
)

func TestDerivedAttributes(t *testing.T) {
	secs := `[{"id":"a","type":"x","title":"T","isVisible":false,"sort_order":0,"extra":1,"content":{"price":9.90,"big":12345678901234567890}},` +
		`{"id":"b","type":"content","sort_order":1,"content":{"body":"<p>b</p>"}}]`
	doc := fromSecs(t, secs)

	t.Run("v1-expressible doc: sections equal what the dual-write stored", func(t *testing.T) {
		// the old mirror: SectionMaps of ToSections of the stored doc
		d, err := builder.ParseExact([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		ss, _ := builder.ToSections(d)
		want, _ := json.Marshal(builder.SectionMaps(ss))

		got := DerivedAttributes(json.RawMessage(`{"title":"t","page_sections":[{"stale":true}],"page_doc":` + doc + `}`))
		var m map[string]json.RawMessage
		if err := json.Unmarshal(got, &m); err != nil {
			t.Fatal(err)
		}
		if string(m["page_sections"]) != string(want) {
			t.Errorf("page_sections:\n%s\nwant\n%s", m["page_sections"], want)
		}
		if !strings.Contains(string(m["page_sections"]), "9.90") || !strings.Contains(string(m["page_sections"]), "12345678901234567890") {
			t.Errorf("exact numbers lost: %s", m["page_sections"])
		}
		if string(m["title"]) != `"t"` || m["page_doc"] == nil {
			t.Errorf("other keys changed: %s", got)
		}
	})
	t.Run("no stored mirror: added", func(t *testing.T) {
		got := DerivedAttributes(json.RawMessage(`{"page_doc":` + doc + `}`))
		if !strings.Contains(string(got), `"page_sections":[{`) {
			t.Errorf("got %s", got)
		}
	})
	t.Run("builder-only doc: the frozen mirror is removed", func(t *testing.T) {
		got := DerivedAttributes(json.RawMessage(`{"page_sections":` + secsA + `,"page_doc":` + twoCol + `}`))
		if strings.Contains(string(got), "page_sections") || !strings.Contains(string(got), "page_doc") {
			t.Errorf("got %s", got)
		}
	})
	t.Run("invalid page_doc: the frozen mirror is dropped (fail closed)", func(t *testing.T) {
		got := DerivedAttributes(json.RawMessage(`{"title":"t","page_sections":` + secsA + `,"page_doc":{"v":99}}`))
		if strings.Contains(string(got), "page_sections") || !strings.Contains(string(got), `"title":"t"`) || !strings.Contains(string(got), `"v":99`) {
			t.Errorf("got %s", got)
		}
	})
	t.Run("untouched without a valid doc", func(t *testing.T) {
		for _, in := range []string{
			`{"page_sections":` + secsA + `}`,
			`{"page_sections":` + secsA + `,"page_doc":null}`,
			`{"title":"x"}`, `[]`, ``, `null`,
		} {
			if got := DerivedAttributes(json.RawMessage(in)); string(got) != in {
				t.Errorf("%q changed to %q", in, got)
			}
		}
	})
}

func TestLegacyPageDoc(t *testing.T) {
	doc, ok := LegacyPageDoc(json.RawMessage(`{"page_sections":` + secsA + `}`))
	if !ok || len(doc.Nodes) != 1 {
		t.Fatalf("doc=%v ok=%v", doc, ok)
	}
	want := obj(t, fromSecs(t, secsA))
	b, _ := json.Marshal(doc)
	if !reflect.DeepEqual(obj(t, string(b)), want) {
		t.Errorf("%s differs from the write conversion", b)
	}
	for _, in := range []string{`{}`, `{"page_sections":"x"}`, `{"page_sections":null}`, `x`, ``} {
		if _, ok := LegacyPageDoc(json.RawMessage(in)); ok {
			t.Errorf("%q: ok", in)
		}
	}
}

func TestEligibleForPageDoc(t *testing.T) {
	for slug, want := range map[string]bool{"page": true, "landing": true, "post": false, "blog": false, "person": false} {
		if got := EligibleForPageDoc(&EntitySchema{Slug: slug}); got != want {
			t.Errorf("%s: %v", slug, got)
		}
	}
	if !EligibleForPageDoc(&EntitySchema{Slug: "guide", Settings: json.RawMessage(`{"editor":"form"}`)}) ||
		EligibleForPageDoc(&EntitySchema{Slug: "guide", Settings: json.RawMessage(`{"editor":"post"}`)}) {
		t.Error("settings.editor not honoured")
	}
}
