//go:build integration

package entity

import (
	"context"
	"encoding/json/jsontext"
	"fmt"
	"slices"
	"testing"

	"github.com/google/uuid"
)

// Site search finds words inside Japanese, Chinese and Korean text (#412),
// the examples from ikotto.org included.
func TestListPublishedFiltered_SearchCJK(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t)
	schemas := NewPostgresSchemaRepository(db)
	sch := fmt.Sprintf("cjk-%s", uuid.NewString()[:8])
	schema, err := schemas.Create(ctx, &CreateSchemaInput{Name: sch, Slug: sch, Schema: jsontext.Value(`{"type":"object","properties":{}}`)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = schemas.Delete(ctx, schema.ID) })
	sfx := uuid.NewString()[:8]
	seed := func(slug, name, attrs string) {
		seedAttrs(t, ctx, schemas, schema.ID, slug+"-"+sfx, name, attrs, "published")
	}
	seed("jazz", "南房総ジャズフェスティバル 2026（みなジャズ）", `{}`)
	seed("film", "映画「大きな家」安房上映会", `{}`)
	seed("body", "お知らせ", `{"content":"<p>本日は<a href=\"/x\" title=\"上映会\">ジャズ</a>の日です。</p>"}`)
	seed("seoul", "서울에서 열리는 축제", `{}`)
	seed("latin", "Jazz night "+sfx, `{"excerpt":"Live music"}`)

	repo := NewPostgresPublicRepository(db)
	find := func(q string) []string {
		t.Helper()
		items, total, err := repo.ListPublishedFiltered(ctx, ListFilter{Search: q, SchemaSlugs: []string{sch}}, 1, 50)
		if err != nil {
			t.Fatalf("search %q: %v", q, err)
		}
		if total != len(items) {
			t.Errorf("search %q: total %d, %d items", q, total, len(items))
		}
		var out []string
		for _, e := range items {
			out = append(out, e.Slug[:len(e.Slug)-len(sfx)-1])
		}
		slices.Sort(out)
		return out
	}
	for _, tc := range []struct {
		q    string
		want []string
	}{
		{"ジャズ", []string{"body", "jazz"}},
		{"みなジャズ", []string{"jazz"}},
		{"上映会", []string{"film"}},
		{"映画", []string{"film"}},
		{"大きな家", []string{"film"}},
		{"会", []string{"film"}},
		{"ジャズ 2026", []string{"jazz"}},
		{"ジャズ -2026", []string{"body"}},
		{"上映会 or 축제", []string{"film", "seoul"}},
		{"서울", []string{"seoul"}},
		{"축제", []string{"seoul"}},
		{"ジャズフェス", []string{"jazz"}},
		{"ジャズ祭", nil},
		{"href", nil}, // tag attributes are never indexed
		{"jazz " + sfx, []string{"latin"}},
	} {
		if got := find(tc.q); !slices.Equal(got, tc.want) {
			t.Errorf("search %q = %v, want %v", tc.q, got, tc.want)
		}
	}
	// The name ranks above the body.
	items, _, err := repo.ListPublishedFiltered(ctx, ListFilter{Search: "ジャズ", SchemaSlugs: []string{sch}}, 1, 50)
	if err != nil || len(items) != 2 || items[0].Name != "南房総ジャズフェスティバル 2026（みなジャズ）" {
		t.Errorf("ranking: %v %v", slugsOf(items), err)
	}
}

// Text without such characters gets exactly the vector it had before the
// bigram index, so Latin-script results and ranking do not move; and the SQL
// class agrees with isSegmentless at every range boundary.
func TestSearchTextFunction(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t)
	conn, err := db.GetConn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	for _, s := range []string{
		"", "Hello World", "café crème brûlée", "2026-11-25 v1.4.4", "Ünïcödé — dashes – and “quotes”",
		"Привет мир", "مرحبا بالعالم", "e-mail foo@example.com https://example.com/a?b=c",
		"<p>Some <b>bold</b> text &amp; <a href=\"/x\">a link</a></p>", "Fullwidth ＡＢＣ １２３",
	} {
		for _, html := range []bool{false, true} {
			var same bool
			if err := conn.QueryRow(ctx, `SELECT to_tsvector('simple', $1) = to_tsvector('simple', hatamoto_search_text($1, $2))`, s, html).Scan(&same); err != nil {
				t.Fatal(err)
			}
			if !same {
				t.Errorf("vector of %q (html=%v) changed", s, html)
			}
		}
	}
	for _, g := range segmentlessRanges {
		for _, r := range []rune{g.Lo - 1, g.Lo, g.Hi, g.Hi + 1} {
			if r >= 0xD800 && r <= 0xDFFF {
				continue // surrogates are not text
			}
			var out string
			s := string([]rune{r, r})
			if err := conn.QueryRow(ctx, `SELECT hatamoto_search_text($1, false)`, s).Scan(&out); err != nil {
				t.Fatalf("U+%04X: %v", r, err)
			}
			if bigram := out == s+" "+string(r); bigram != isSegmentless(r) {
				t.Errorf("U+%04X: SQL gives %q, isSegmentless = %v", r, out, isSegmentless(r))
			}
		}
	}
}
