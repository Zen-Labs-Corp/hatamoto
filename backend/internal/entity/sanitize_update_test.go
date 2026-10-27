package entity

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/testutil"
)

// Legacy markup the rich-text policy would rewrite (onclick is stripped).
const legacyHTML = `<p>hi</p><button onclick="go()">x</button>`

func TestSanitizeAttributesAgainst_UnchangedIsByteIdentical(t *testing.T) {
	// Odd spacing and key order prove the bytes are returned verbatim.
	stored := json.RawMessage(`{ "content" : "` + strings.ReplaceAll(legacyHTML, `"`, `\"`) + `", "page_sections":[{"id":"a","type":"html","content":{"body":"` + strings.ReplaceAll(legacyHTML, `"`, `\"`) + `"}}] }`)
	out, err := sanitizeAttributesAgainst(stored, stored)
	testutil.RequireNoError(t, err)
	testutil.Equal(t, string(stored), string(out))

	// The create path (no stored blob) still sanitises the same input.
	out, err = sanitizeAttributes(stored)
	testutil.RequireNoError(t, err)
	testutil.NotContains(t, string(out), "onclick")
}

func TestSanitizeAttributesAgainst_ChangedValuesAreSanitised(t *testing.T) {
	stored := json.RawMessage(`{"content":"<p>old</p>","other":{"content":"<p>keep</p>"}}`)
	in := json.RawMessage(`{"content":"<p>new</p><script>alert(1)</script>","other":{"content":"<p>keep</p>"}}`)
	out, err := sanitizeAttributesAgainst(in, stored)
	testutil.RequireNoError(t, err)
	testutil.NotContains(t, string(out), "<script")
	testutil.Contains(t, string(out), "new")
}

func TestSanitizeAttributesAgainst_OnlyChangedPathsTouched(t *testing.T) {
	// "legacy" is unchanged (kept raw); "fresh" differs and is sanitised.
	stored := json.RawMessage(`{"legacy":{"content":"` + `<p onclick=\"x()\">l</p>` + `"},"fresh":{"content":"<p>a</p>"}}`)
	in := json.RawMessage(`{"legacy":{"content":"` + `<p onclick=\"x()\">l</p>` + `"},"fresh":{"content":"<p onclick=\"y()\">a</p>"}}`)
	out, err := sanitizeAttributesAgainst(in, stored)
	testutil.RequireNoError(t, err)
	var got struct {
		Legacy struct{ Content string } `json:"legacy"`
		Fresh  struct{ Content string } `json:"fresh"`
	}
	testutil.RequireNoError(t, json.Unmarshal(out, &got))
	testutil.Contains(t, got.Legacy.Content, "onclick")
	testutil.NotContains(t, got.Fresh.Content, "onclick")
}

func TestSanitizeAttributesAgainst_Sections(t *testing.T) {
	esc := func(s string) string { return strings.ReplaceAll(s, `"`, `\"`) }
	section := func(id, body string) string {
		return `{"id":"` + id + `","type":"html","content":{"body":"` + esc(body) + `"}}`
	}
	stored := json.RawMessage(`{"page_sections":[` + section("a", legacyHTML) + `,` + section("b", `<p>b</p>`) + `]}`)

	t.Run("moved section with same id is not resanitised", func(t *testing.T) {
		in := json.RawMessage(`{"page_sections":[` + section("b", `<p>b</p>`) + `,` + section("a", legacyHTML) + `]}`)
		out, err := sanitizeAttributesAgainst(in, stored)
		testutil.RequireNoError(t, err)
		testutil.Equal(t, string(in), string(out))
	})

	t.Run("changed body is sanitised", func(t *testing.T) {
		in := json.RawMessage(`{"page_sections":[` + section("a", legacyHTML+`<script>x</script>`) + `,` + section("b", `<p>b</p>`) + `]}`)
		out, err := sanitizeAttributesAgainst(in, stored)
		testutil.RequireNoError(t, err)
		testutil.NotContains(t, string(out), "<script")
		testutil.NotContains(t, string(out), "onclick") // changed section is wholly resanitised
	})

	t.Run("new section is sanitised", func(t *testing.T) {
		in := json.RawMessage(`{"page_sections":[` + section("a", legacyHTML) + `,` + section("c", legacyHTML) + `]}`)
		out, err := sanitizeAttributesAgainst(in, stored)
		testutil.RequireNoError(t, err)
		var got struct {
			Sections []struct {
				ID      string
				Content struct{ Body string }
			} `json:"page_sections"`
		}
		testutil.RequireNoError(t, json.Unmarshal(out, &got))
		testutil.Contains(t, got.Sections[0].Content.Body, "onclick")
		testutil.NotContains(t, got.Sections[1].Content.Body, "onclick")
	})

	t.Run("id-less sections match by index", func(t *testing.T) {
		noID := func(body string) string { return `{"type":"html","content":{"body":"` + esc(body) + `"}}` }
		st := json.RawMessage(`{"page_sections":[` + noID(legacyHTML) + `]}`)
		out, err := sanitizeAttributesAgainst(st, st)
		testutil.RequireNoError(t, err)
		testutil.Equal(t, string(st), string(out))
	})
}

func TestService_UpdateEntity_UnchangedSaveKeepsLegacyBytes(t *testing.T) {
	t.Parallel()
	svc, sr, er := newTestService()
	schema := seedSchema(t, sr)
	legacy := json.RawMessage(`{ "content":"<p>x</p><button onclick=\"go()\">b</button>" }`)
	existing := seedEntity(t, er, schema, func(e *Entity) { e.Attributes = legacy })

	same := append(json.RawMessage(nil), legacy...)
	updated, err := svc.UpdateEntity(context.Background(), existing.ID, &testUserID, &UpdateEntityInput{Attributes: &same})
	testutil.RequireNoError(t, err)
	testutil.Equal(t, string(legacy), string(updated.Attributes))

	changed := json.RawMessage(`{"content":"<p>x</p><button onclick=\"go()\">b</button><script>1</script>"}`)
	updated, err = svc.UpdateEntity(context.Background(), existing.ID, &testUserID, &UpdateEntityInput{Attributes: &changed})
	testutil.RequireNoError(t, err)
	testutil.NotContains(t, string(updated.Attributes), "<script")
	testutil.NotContains(t, string(updated.Attributes), "onclick")
}

// A non-html section stores its body raw; switching its type to html (or an
// alias) with the body unchanged must not count as "unchanged".
func TestSanitizeAttributesAgainst_TypeSwitchIsSanitised(t *testing.T) {
	t.Setenv(htmlSectionAliasesEnv, "legacy_type")
	const evil = `<img src=x onerror=alert(1)><script>x</script>`
	sec := func(id, typ string) string {
		idPart := ""
		if id != "" {
			idPart = `"id":"` + id + `",`
		}
		return `{"page_sections":[{` + idPart + `"type":"` + typ + `","content":{"body":"` + evil + `"}}]}`
	}
	cases := []struct{ name, id, from, to string }{
		{"by id text to html", "a", "text", "html"},
		{"by index text to html", "", "text", "html"},
		{"by id code to html", "a", "code", "html"},
		{"by id text to alias", "a", "text", "legacy_type"},
		{"by index text to alias", "", "text", "legacy_type"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stored := json.RawMessage(sec(c.id, c.from))
			out, err := sanitizeAttributesAgainst(json.RawMessage(sec(c.id, c.to)), stored)
			testutil.RequireNoError(t, err)
			testutil.NotContains(t, string(out), "<script")
			testutil.NotContains(t, string(out), "onerror")
		})
	}

	t.Run("alias to alias unchanged keeps bytes", func(t *testing.T) {
		st := json.RawMessage(sec("a", "legacy_type"))
		out, err := sanitizeAttributesAgainst(st, st)
		testutil.RequireNoError(t, err)
		testutil.Equal(t, string(st), string(out))
	})
}
