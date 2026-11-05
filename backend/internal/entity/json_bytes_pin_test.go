package entity

import (
	"strings"
	"testing"
)

// These tests pin the exact bytes the entity JSON helpers produce, as the v1
// encoder wrote them, so a change of JSON library cannot alter stored content.

func TestSanitizeUnchangedReturnsInputBytes(t *testing.T) {
	cases := []string{
		"{ \"title\" : \"x\",\n  \"content\": \"<p>fine</p>\",  \"b\":1 , \"a\":[ 1 , 2 ] }",
		`{"z":1,"a":{"y":2,"b":3},"content":"<p>ok <b>bold</b></p>"}`,
		`{"n":9007199254740993,"f":1.50,"e":1e3,"s":"<&>"}`,
		"  {\"k\":\"v\"}\n",
	}
	for _, in := range cases {
		out, err := sanitizeAttributesAgainst([]byte(in), nil)
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != in {
			t.Errorf("unchanged input rewritten:\n got %q\nwant %q", out, in)
		}
	}
}

func TestSanitizeKeepsExactNumbers(t *testing.T) {
	in := `{"content":"<p>x</p><script>alert(1)</script>","n":9007199254740993,"f":1.50,"e":1e3,"neg":-0.0}`
	out, err := sanitizeAttributesAgainst([]byte(in), nil)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"content":"\u003cp\u003ex\u003c/p\u003e","e":1e3,"f":1.50,"n":9007199254740993,"neg":-0.0}`
	if string(out) != want {
		t.Errorf("got  %s\nwant %s", out, want)
	}
}

func TestCustomJSONLDDocsEscapesScriptClose(t *testing.T) {
	attrs := []byte(`{"custom_jsonld":"{\"@context\":\"https://schema.org\",\"@type\":\"Thing\",\"name\":\"</script><b>x & y\"}"}`)
	docs := CustomJSONLDDocs(attrs)
	if len(docs) != 1 {
		t.Fatalf("docs = %d, want 1", len(docs))
	}
	const want = `{"@context":"https://schema.org","@type":"Thing","name":"\u003c/script\u003e\u003cb\u003ex \u0026 y"}`
	if string(docs[0]) != want {
		t.Errorf("got  %s\nwant %s", docs[0], want)
	}
	if strings.Contains(string(docs[0]), "<") {
		t.Errorf("raw '<' in %s", docs[0])
	}
}

func TestCustomJSONLDDocsArrayKeySorted(t *testing.T) {
	attrs := []byte(`{"custom_jsonld":"[{\"@type\":\"A\",\"@context\":\"c\",\"z\":1.50,\"a\":null},{\"@type\":\"B\",\"@context\":\"c\"}]"}`)
	docs := CustomJSONLDDocs(attrs)
	if len(docs) != 2 {
		t.Fatalf("docs = %d, want 2", len(docs))
	}
	if got, want := string(docs[0]), `{"@context":"c","@type":"A","a":null,"z":1.50}`; got != want {
		t.Errorf("doc 0 = %s, want %s", got, want)
	}
	if got, want := string(docs[1]), `{"@context":"c","@type":"B"}`; got != want {
		t.Errorf("doc 1 = %s, want %s", got, want)
	}
}

func TestJSONRawEqualPinned(t *testing.T) {
	cases := []struct {
		name, a, b string
		want       bool
	}{
		{"key order and whitespace", `{"a":1,"b":[1,2]}`, "{ \"b\" : [1, 2],\n\"a\":1 }", true},
		{"big integers differ", `{"n":9007199254740993}`, `{"n":9007199254740992}`, false},
		{"big integers equal", `{"n":9007199254740993}`, `{ "n": 9007199254740993 }`, true},
		{"trailing zero is a different number", `{"f":1.5}`, `{"f":1.50}`, false},
		{"same trailing zero", `{"f":1.50}`, `{"f":1.50}`, true},
		{"null vs missing", `{"a":null}`, `{}`, false},
		{"invalid", `{`, `{}`, false},
	}
	for _, c := range cases {
		if got := jsonRawEqual([]byte(c.a), []byte(c.b)); got != c.want {
			t.Errorf("%s: jsonRawEqual(%s, %s) = %v, want %v", c.name, c.a, c.b, got, c.want)
		}
	}
}

func TestJSONEqualPinned(t *testing.T) {
	cases := []struct {
		name string
		a, b any
		want bool
	}{
		{"map key order", map[string]any{"b": 1, "a": 2}, map[string]any{"a": 2, "b": 1}, true},
		{"nested", map[string]any{"a": []any{1, "x"}}, map[string]any{"a": []any{1, "x"}}, true},
		{"different value", map[string]any{"a": 1}, map[string]any{"a": 2}, false},
		{"int and float of one value", 1, 1.0, true},
		{"html chars", "<b>&", "<b>&", true},
		{"nil and empty slice", []string(nil), []string{}, false},
		{"unencodable", make(chan int), 1, false},
	}
	for _, c := range cases {
		if got := jsonEqual(c.a, c.b); got != c.want {
			t.Errorf("%s: jsonEqual = %v, want %v", c.name, got, c.want)
		}
	}
}
