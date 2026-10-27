package entity

import (
	"bytes"
	"encoding/json"
	"errors"
	"hatamoto.dev/core/internal/builder"
	"reflect"
	"strings"
	"testing"
)

const (
	twoCol = `{"v":2,"nodes":[{"id":"r","t":"container","p":{"tag":"div"},"c":[` +
		`{"id":"h","t":"heading","p":{"text":"Hi","level":2}}]}]}`
	secsA = `[{"id":"a","type":"content","content":{"body":"<p>one</p>"},"sort_order":0}]`
	secsB = `[{"id":"a","type":"content","content":{"body":"<p>two</p>"},"sort_order":0}]`
)

func obj(t *testing.T, s string) map[string]any {
	t.Helper()
	m, ok := decodeObject([]byte(s))
	if !ok {
		t.Fatalf("bad json %s", s)
	}
	return m
}

func runNorm(t *testing.T, mode docMode, stored, in string) (map[string]any, error) {
	t.Helper()
	return runNormFor(t, mode, stored, in, true)
}

func runNormFor(t *testing.T, mode docMode, stored, in string, eligible bool) (map[string]any, error) {
	t.Helper()
	var st map[string]any
	if stored != "" {
		st = obj(t, stored)
	}
	return normalizeDoc(mode, st, obj(t, in), eligible)
}

// fromSecs is the doc the sections convert to.
func fromSecs(t *testing.T, secs string) string {
	t.Helper()
	doc, err := docFromSectionsValue(obj(t, `{"s":`+secs+`}`)["s"])
	if err != nil {
		t.Fatal(err)
	}
	return plainJSON(t, doc)
}

func TestNormalizeDoc_DocWrite(t *testing.T) {
	t.Run("doc write ignores the incoming page_sections", func(t *testing.T) {
		for _, doc := range []string{twoCol, fromSecs(t, secsA)} {
			out, err := runNorm(t, docWrite, "", `{"title":"x","page_doc":`+doc+`,"page_sections":`+secsB+`}`)
			if err != nil {
				t.Fatal(err)
			}
			if _, has := out["page_sections"]; has {
				t.Error("incoming page_sections copied to the output")
			}
			if out["title"] != "x" || out["page_doc"] == nil {
				t.Errorf("out=%v", out)
			}
		}
	})
	t.Run("the stored mirror is carried forward verbatim", func(t *testing.T) {
		stored := `{"page_sections":` + secsA + `,"page_doc":` + fromSecs(t, secsA) + `}`
		for _, doc := range []string{twoCol, fromSecs(t, secsB)} {
			out, err := runNorm(t, docWrite, stored, `{"page_doc":`+doc+`,"page_sections":`+secsB+`}`)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(out["page_sections"], obj(t, stored)["page_sections"]) {
				t.Errorf("mirror changed: %v", out["page_sections"])
			}
		}
	})
	t.Run("component content is sanitised", func(t *testing.T) {
		doc := `{"v":2,"nodes":[{"id":"c","t":"component","p":{"type":"html","content":{"body":"<p onclick=\"x()\">hi</p><script>x</script>"}}}]}`
		out, err := runNorm(t, docWrite, "", `{"page_doc":`+doc+`}`)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(out["page_doc"])
		if strings.Contains(string(b), "script") || strings.Contains(string(b), "onclick") {
			t.Errorf("not sanitised: %s", b)
		}
	})
	t.Run("invalid doc is ErrInvalidDoc and not ErrValidationFailed", func(t *testing.T) {
		for _, d := range []string{`{"v":1,"nodes":[]}`, `{"v":2,"nodes":[{"id":"x","t":"marquee","p":{}}]}`, `null`, `"s"`} {
			_, err := runNorm(t, docWrite, "", `{"page_doc":`+d+`}`)
			if !errors.Is(err, ErrInvalidDoc) || errors.Is(err, ErrValidationFailed) {
				t.Errorf("%s: err=%v", d, err)
			}
		}
	})
}

// The D2 write matrix for pages that already have a doc.
func TestNormalizeDoc_StoredDoc(t *testing.T) {
	v1 := fromSecs(t, secsA)
	stored := `{"page_sections":` + secsA + `,"page_doc":` + v1 + `}`
	builderOnly := `{"page_doc":` + twoCol + `}`

	t.Run("changed page_sections is refused", func(t *testing.T) {
		for _, st := range []string{stored, builderOnly} {
			_, err := runNorm(t, docWrite, st, `{"page_sections":`+secsB+`,"page_doc":`+obj2doc(t, st)+`}`)
			if !errors.Is(err, ErrPageSectionsRemoved) || errors.Is(err, ErrValidationFailed) {
				t.Errorf("stored %s: err=%v", st, err)
			}
			_, err = runNorm(t, docWrite, st, `{"page_sections":`+secsB+`}`)
			if !errors.Is(err, ErrPageSectionsRemoved) {
				t.Errorf("stored %s, no doc in input: err=%v", st, err)
			}
		}
	})
	t.Run("page_sections null or empty is a change", func(t *testing.T) {
		for _, v := range []string{`null`, `[]`, `"x"`} {
			if _, err := runNorm(t, docWrite, stored, `{"page_sections":`+v+`}`); !errors.Is(err, ErrPageSectionsRemoved) {
				t.Errorf("%s: err=%v", v, err)
			}
		}
	})
	t.Run("round trip of the stored mirror is a no-op", func(t *testing.T) {
		out, err := runNorm(t, docWrite, stored, stored)
		if err != nil || !reflect.DeepEqual(out, obj(t, stored)) {
			t.Errorf("out=%v err=%v", out, err)
		}
	})
	t.Run("round trip of derived sections is accepted", func(t *testing.T) {
		// the mirror is frozen and stale; a plugin read the derived sections
		st := `{"page_sections":` + secsB + `,"page_doc":` + v1 + `}`
		out, err := runNorm(t, docWrite, st, `{"page_sections":`+secsA+`,"page_doc":`+v1+`}`)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(out["page_sections"], obj(t, st)["page_sections"]) {
			t.Errorf("frozen mirror not carried: %v", out["page_sections"])
		}
	})
	t.Run("omitted page_sections: stored mirror and doc carried forward", func(t *testing.T) {
		out, err := runNorm(t, docWrite, stored, `{"title":"y"}`)
		if err != nil {
			t.Fatal(err)
		}
		want := obj(t, stored)
		if !reflect.DeepEqual(out["page_doc"], want["page_doc"]) || !reflect.DeepEqual(out["page_sections"], want["page_sections"]) || out["title"] != "y" {
			t.Errorf("out=%v", out)
		}
		out, err = runNorm(t, docWrite, builderOnly, `{"title":"y"}`)
		if err != nil || out["page_doc"] == nil || out["page_sections"] != nil {
			t.Errorf("builder-only: out=%v err=%v", out, err)
		}
	})
	t.Run("page_doc wins over a different page_sections", func(t *testing.T) {
		out, err := runNorm(t, docWrite, stored, `{"page_sections":`+secsA+`,"page_doc":`+twoCol+`}`)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(out["page_sections"], obj(t, stored)["page_sections"]) || !strings.Contains(plainJSON(t, out["page_doc"]), `"heading"`) {
			t.Errorf("out=%v", out)
		}
	})
}

// obj2doc is the page_doc of a stored attributes JSON.
func obj2doc(t *testing.T, attrs string) string { return plainJSON(t, obj(t, attrs)["page_doc"]) }

// Convert-once: no doc anywhere, a page_sections array exists.
func TestNormalizeDoc_ConvertOnce(t *testing.T) {
	t.Run("create with sections stores the doc and no mirror", func(t *testing.T) {
		out, err := runNorm(t, docWrite, "", `{"title":"t","page_sections":`+secsA+`}`)
		if err != nil {
			t.Fatal(err)
		}
		if _, has := out["page_sections"]; has || out["page_doc"] == nil || out["title"] != "t" {
			t.Errorf("out=%v", out)
		}
		if got, want := plainJSON(t, out["page_doc"]), fromSecs(t, secsA); !reflect.DeepEqual(obj(t, got), obj(t, want)) {
			t.Errorf("doc differs from the conversion:\n%s\n%s", got, want)
		}
	})
	t.Run("unconverted page: the mirror is carried, the write's sections win", func(t *testing.T) {
		out, err := runNorm(t, docWrite, `{"page_sections":`+secsA+`}`, `{"page_sections":`+secsB+`}`)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(out["page_sections"], obj(t, `{"page_sections":`+secsA+`}`)["page_sections"]) {
			t.Errorf("mirror changed: %v", out["page_sections"])
		}
		if !strings.Contains(plainJSON(t, out["page_doc"]), "two") {
			t.Errorf("doc is not the write's: %v", out["page_doc"])
		}
	})
	t.Run("unrelated write converts the stored sections", func(t *testing.T) {
		out, err := runNorm(t, docWrite, `{"page_sections":`+secsA+`}`, `{"page_sections":`+secsA+`,"title":"n"}`)
		if err != nil || out["page_doc"] == nil || out["page_sections"] == nil {
			t.Errorf("out=%v err=%v", out, err)
		}
	})
	t.Run("a conversion failure does not block an unrelated write", func(t *testing.T) {
		bad := `{"page_sections":"junk","title":"n"}`
		out, err := runNorm(t, docWrite, `{"page_sections":"junk"}`, bad)
		if err != nil || !reflect.DeepEqual(out, obj(t, bad)) {
			t.Errorf("out=%v err=%v", out, err)
		}
		if _, err := runNorm(t, docWrite, "", bad); !errors.Is(err, ErrInvalidDoc) {
			t.Errorf("write carrying page_sections: err=%v", err)
		}
	})
	t.Run("unconvertible ids: unrelated writes leave the attributes untouched, no empty doc", func(t *testing.T) {
		const dup = `[{"id":"a","type":"html","content":{"html":"<p>x</p>"}},{"id":"a","type":"html","content":{"html":"<p>y</p>"}}]`
		stored := `{"page_sections":` + dup + `}`
		for _, in := range []string{stored, `{"title":"n","page_sections":` + dup + `}`, `{"title":"n"}`} {
			out, err := runNorm(t, docWrite, stored, in)
			if err != nil || out["page_doc"] != nil || !reflect.DeepEqual(out["page_sections"], obj(t, stored)["page_sections"]) && strings.Contains(in, "page_sections") {
				t.Errorf("%s: out=%v err=%v", in, out, err)
			}
		}
		if _, changed := convertUnconverted(&Entity{Attributes: json.RawMessage(stored)}); changed {
			t.Error("lazy conversion of an unconvertible page changed it")
		}
	})
	t.Run("no sections anywhere: untouched", func(t *testing.T) {
		in := `{"title":"t"}`
		out, err := runNorm(t, docWrite, `{"x":1}`, in)
		if err != nil || !reflect.DeepEqual(out, obj(t, in)) {
			t.Errorf("out=%v err=%v", out, err)
		}
	})
	t.Run("ineligible entities are passthrough", func(t *testing.T) {
		in := `{"page_sections":` + secsB + `}`
		for _, st := range []string{`{"page_sections":` + secsA + `}`, `{"page_sections":` + secsA + `,"page_doc":` + twoCol + `}`} {
			out, err := runNormFor(t, docWrite, st, in, false)
			if err != nil || out["page_doc"] != nil && !strings.Contains(st, "page_doc") || !reflect.DeepEqual(out["page_sections"], obj(t, in)["page_sections"]) {
				t.Errorf("stored %s: out=%v err=%v", st, out, err)
			}
		}
	})
}

func TestNormalizeDocRaw_NoOpIsByteIdentical(t *testing.T) {
	doc := fromSecs(t, secsA)
	stored := json.RawMessage(`{"title":"t","page_sections":` + secsA + `,"page_doc":` + doc + `}`)
	got, err := normalizeDocRaw(docWrite, stored, stored, true)
	if err != nil || string(got) != string(stored) {
		t.Fatalf("got %s err=%v", got, err)
	}
	// without the mirror in the input, the stored one is carried: same value
	got, err = normalizeDocRaw(docWrite, stored, json.RawMessage(`{"title":"t","page_doc":`+doc+`}`), true)
	if err != nil || !jsonRawEqual(got, stored) {
		t.Errorf("got %s err=%v", got, err)
	}
}

func TestNormalizeDoc_Idempotent(t *testing.T) {
	doc := fromSecs(t, secsA)
	cases := []struct{ name, stored, in string }{
		{"doc write", "", `{"page_doc":` + twoCol + `}`},
		{"doc write expressible", "", `{"page_doc":` + doc + `}`},
		{"doc write over a mirror", `{"page_sections":` + secsA + `,"page_doc":` + doc + `}`, `{"page_sections":` + secsB + `,"page_doc":` + twoCol + `}`},
		{"carry forward", `{"page_doc":` + twoCol + `}`, `{"x":1}`},
		{"convert create", "", `{"page_sections":` + secsA + `}`},
		{"convert unconverted", `{"page_sections":` + secsA + `}`, `{"page_sections":` + secsB + `}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var stored map[string]any
			if c.stored != "" {
				stored = obj(t, c.stored)
			}
			one, err := normalizeDoc(docWrite, stored, obj(t, c.in), true)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(one)
			two, err := normalizeDoc(docWrite, stored, obj(t, string(b)), true)
			if err != nil {
				t.Fatal(err)
			}
			b1, _ := json.Marshal(one)
			b2, _ := json.Marshal(two)
			if string(b1) != string(b2) {
				t.Errorf("not idempotent:\n%s\n%s", b1, b2)
			}
		})
	}
}

func TestNormalizeDoc_Restore(t *testing.T) {
	doc := fromSecs(t, secsA)
	t.Run("pre-doc blob, current has a doc: converts, never errors", func(t *testing.T) {
		for _, st := range []string{`{"page_doc":` + twoCol + `}`, `{"page_sections":` + secsA + `,"page_doc":` + doc + `}`} {
			out, err := runNorm(t, docRestore, st, `{"page_sections":`+secsB+`}`)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(plainJSON(t, out["page_doc"]), "two") {
				t.Errorf("doc=%v", out["page_doc"])
			}
		}
	})
	t.Run("pre-doc blob, current has none: converts", func(t *testing.T) {
		out, err := runNorm(t, docRestore, `{"page_sections":`+secsA+`}`, `{"page_sections":`+secsB+`}`)
		if err != nil || !strings.Contains(plainJSON(t, out["page_doc"]), "two") {
			t.Errorf("out=%v err=%v", out, err)
		}
	})
	t.Run("blob with doc: doc write even when equal to current", func(t *testing.T) {
		st := `{"page_sections":` + secsA + `,"page_doc":` + doc + `}`
		out, err := runNorm(t, docRestore, st, `{"page_doc":`+doc+`,"page_sections":`+secsB+`}`)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(out["page_sections"], obj(t, st)["page_sections"]) {
			t.Errorf("mirror not carried: %v", out["page_sections"])
		}
	})
	t.Run("pre-doc blob whose sections do not convert: the conversion error", func(t *testing.T) {
		for _, st := range []string{`{"page_doc":` + twoCol + `}`, `{"page_sections":` + secsA + `}`} {
			for _, bad := range []string{`[{"id":"a"},{"id":"a"}]`, `[{"id":"hero.1"}]`, `[{"id":"Hero section"}]`} {
				if _, err := runNorm(t, docRestore, st, `{"page_sections":`+bad+`}`); !errors.Is(err, ErrInvalidDoc) {
					t.Errorf("%s over %s: err=%v", bad, st, err)
				}
			}
		}
	})
	t.Run("invalid doc in blob", func(t *testing.T) {
		_, err := runNorm(t, docRestore, "", `{"page_doc":{"v":9}}`)
		if !errors.Is(err, ErrInvalidDoc) {
			t.Errorf("err=%v", err)
		}
	})
}

func TestNormalizeDoc_InputNotMutated(t *testing.T) {
	in := obj(t, `{"page_doc":`+twoCol+`,"page_sections":`+secsA+`}`)
	want := obj(t, `{"page_doc":`+twoCol+`,"page_sections":`+secsA+`}`)
	if _, err := normalizeDoc(docWrite, nil, in, true); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, want) {
		t.Error("input mutated")
	}
}

// Unchanged legacy content is never re-sanitised: not when a page without a
// doc gets one.
func TestNormalizeDoc_UnchangedLegacyContentByteIdentical(t *testing.T) {
	const legacy = `<div onclick=\"x()\">hi</div><script>t()</script>`
	secs := `[{"id":"a","type":"html","content":{"body":"` + legacy + `"},"sort_order":0},` +
		`{"id":"b","type":"content","content":{"body":"<p>b</p>"},"sort_order":1}]`
	stored := `{"page_sections":` + secs + `}`

	// an unrelated write on an unconverted page
	out, err := runNorm(t, docWrite, stored, stored)
	if err != nil {
		t.Fatal(err)
	}
	if b := plainJSON(t, out["page_doc"]); strings.Count(b, legacy) != 1 {
		t.Errorf("convert rewrote legacy content: %s", b)
	}
	// converted by the page_doc a client sends with the same content
	secList, err := sectionsFromAny(obj(t, stored)["page_sections"])
	if err != nil {
		t.Fatal(err)
	}
	conv, err := runNorm(t, docWrite, stored, `{"page_doc":`+plainJSON(t, builder.FromSections(secList))+`}`)
	if err != nil {
		t.Fatal(err)
	}
	if b := plainJSON(t, conv); strings.Count(b, legacy) != 2 {
		t.Errorf("convert rewrote legacy content: %s", b)
	}
}

// plainJSON marshals without HTML escaping, so markup can be searched for.
func plainJSON(t *testing.T, v any) string {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// A doc write keeps every digit of numbers and every unknown section key.
func TestNormalizeDocRaw_ExactNumbersAndExtraKeys(t *testing.T) {
	in := `{"page_doc":{"v":2,"nodes":[{"id":"c-a","t":"container","p":{"tag":"section","v1":true},"c":[` +
		`{"id":"a","t":"component","p":{"type":"x","extra":{"anchor":"top"},"content":{"big":12345678901234567890,"price":9.90}}}]}]}}`
	out, err := normalizeDocRaw(docWrite, nil, json.RawMessage(in), true)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if m, _ = decodeObject(out); m == nil {
		t.Fatal("bad output")
	}
	got := plainJSON(t, m)
	for _, want := range []string{"12345678901234567890", "9.90", `"anchor":"top"`} {
		if strings.Count(got, want) != 1 {
			t.Errorf("%s: want one copy in %s", want, got)
		}
	}
}

// Fields the theme schema marks richtext/html are sanitised on a page_doc
// write, like a v1 write.
func TestNormalizeDoc_SchemaFieldsSanitised(t *testing.T) {
	fakeKinds(t)
	const evil = `<p>hi</p><img src=x onerror=alert(1)>`
	doc := `{"v":2,"nodes":[{"id":"c-a","t":"container","p":{"tag":"section","v1":true},"c":[` +
		`{"id":"a","t":"component","p":{"type":"split","content":{"lede":"` + evil + `"}}}]}]}`
	out, err := runNorm(t, docWrite, "", `{"page_doc":`+doc+`}`)
	if err != nil {
		t.Fatal(err)
	}
	if b := plainJSON(t, out); strings.Contains(b, "onerror") {
		t.Errorf("unsanitised schema field stored (doc or mirror): %s", b)
	}

	// builder-only layout: the doc itself is still sanitised
	only := `{"v":2,"nodes":[{"id":"r","t":"container","p":{"tag":"div"},"c":[` +
		`{"id":"a","t":"component","p":{"type":"split","content":{"lede":"` + evil + `"}}}]}]}`
	out, err = runNorm(t, docWrite, "", `{"page_doc":`+only+`}`)
	if err != nil {
		t.Fatal(err)
	}
	if b := plainJSON(t, out); strings.Contains(b, "onerror") {
		t.Errorf("unsanitised schema field in builder-only doc: %s", b)
	}
}

// A stored doc with non-canonical numbers, saved back whole by a float64
// client (a decoding hook or old tooling): that is a no-op that keeps the exact
// numbers, and an edit made through page_sections is refused, not half-applied.
func TestNormalizeDocRaw_Float64ClientRoundTrip(t *testing.T) {
	secs := `[{"id":"a","type":"x","sort_order":0,"content":{"body":"OLD","rating":4.0,"price":9.90,"big":12345678901234567890}}]`
	stored, err := normalizeDocRaw(docWrite, nil, json.RawMessage(`{"page_doc":`+fromSecs(t, secs)+`}`), true)
	if err != nil {
		t.Fatal(err)
	}
	stored = json.RawMessage(strings.Replace(string(stored), `{"page_doc"`, `{"page_sections":`+secs+`,"page_doc"`, 1))

	var m map[string]any
	if err := json.Unmarshal(stored, &m); err != nil { // float64 numbers
		t.Fatal(err)
	}
	in, _ := json.Marshal(m)
	out, err := normalizeDocRaw(docWrite, stored, in, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "9.90") || !strings.Contains(string(out), "12345678901234567890") {
		t.Errorf("exact numbers lost: %s", out)
	}

	m["page_sections"].([]any)[0].(map[string]any)["content"].(map[string]any)["body"] = "NEW"
	in, _ = json.Marshal(m)
	if _, err := normalizeDocRaw(docWrite, stored, in, true); !errors.Is(err, ErrPageSectionsRemoved) {
		t.Errorf("edit through page_sections: err=%v", err)
	}
}

// JSON may spell the key "page\u005fdoc"; it is still the doc and must be
// validated and sanitised.
func TestNormalizeDocRaw_EscapedDocKey(t *testing.T) {
	bad := `{"page_sections":[],"page\u005fdoc":{"v":99,"nodes":"junk"}}`
	if _, err := normalizeDocRaw(docWrite, nil, json.RawMessage(bad), true); !errors.Is(err, ErrInvalidDoc) {
		t.Errorf("invalid doc under escaped key: err=%v, want ErrInvalidDoc", err)
	}

	evil := `{"page\u005fdoc":{"v":2,"nodes":[{"id":"r","t":"container","p":{"tag":"div"},"c":[` +
		`{"id":"a","t":"component","p":{"type":"html","content":{"body":"<script>x()</script><p>ok</p>","content":"<img src=x onerror=alert(1)>"}}}]}]}}`
	out, err := normalizeDocRaw(docWrite, nil, json.RawMessage(evil), true)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(out); strings.Contains(s, "<script") || strings.Contains(s, "onerror") || !strings.Contains(s, `"page_doc"`) {
		t.Errorf("escaped-key doc not sanitised: %s", out)
	}
}

func TestNormalizeDocRaw_CaseVariantKeysRejected(t *testing.T) {
	for _, key := range []string{"Page_Doc", "PAGE_DOC", "page_DOC", "Page_Sections", `page\u005fDoc`} {
		in := `{"page_sections":[],"` + key + `":{"v":2,"nodes":[]}}`
		for _, mode := range []docMode{docWrite, docRestore} {
			if _, err := normalizeDocRaw(mode, nil, json.RawMessage(in), true); !errors.Is(err, ErrInvalidDoc) {
				t.Errorf("key %s mode %v: err=%v, want ErrInvalidDoc", key, mode, err)
			}
		}
	}
	if _, err := normalizeDocRaw(docWrite, nil, json.RawMessage(`{"page_sections":[]}`), true); err != nil {
		t.Errorf("exact keys rejected: %v", err)
	}
}

func TestNormalizeDoc_ElementSettings(t *testing.T) {
	two := func(a, b string) string {
		return `{"v":2,"nodes":[{"id":"a","t":"container","p":{"anchor":"` + a + `"}},{"id":"b","t":"container","p":{"anchor":"` + b + `"}}]}`
	}
	if _, err := runNorm(t, docWrite, "", `{"page_doc":`+two("one", "two")+`}`); err != nil {
		t.Fatalf("distinct anchors: %v", err)
	}
	for name, doc := range map[string]string{
		"duplicate":            two("one", "one"),
		"reserved":             two("one", "main-content"),
		"node id":              two("one", "a"),
		"link around a button": `{"v":2,"nodes":[{"id":"a","t":"container","p":{"href":"/x"},"c":[{"id":"b","t":"button","p":{"text":"Go","href":"/y"}}]}]}`,
	} {
		_, err := runNorm(t, docWrite, "", `{"page_doc":`+doc+`}`)
		if !errors.Is(err, ErrInvalidDoc) {
			t.Errorf("%s: err = %v, want ErrInvalidDoc", name, err)
		}
	}
}
