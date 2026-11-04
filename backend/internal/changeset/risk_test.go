package changeset

import (
	"encoding/json"
	"testing"
)

func riskEntity(slug string, attrs map[string]any) Item {
	return Item{Kind: KindEntity, Op: OpUpsert, Payload: rawJSON(map[string]any{"name": "N", "slug": slug, "attributes": attrs})}
}

func seoEntity(slug string, seo *EntitySEO) Item {
	return Item{Kind: KindEntity, Op: OpUpsert, Payload: rawJSON(map[string]any{"name": "N", "slug": slug, "attributes": map[string]any{"content": "t"}, "seo": seo})}
}

func rawJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func liveOf(slug string, attrs map[string]any) *Live {
	return &Live{Slug: slug, Attributes: rawJSON(attrs)}
}

func TestRiskClassify(t *testing.T) {
	env := Env{SiteHosts: map[string]bool{"example.org": true}}
	body := func(s string) map[string]any { return map[string]any{"content": s} }
	tests := []struct {
		name string
		it   Item
		live *Live
		want Verdict
	}{
		{"plain text create", riskEntity("a", body("Hello world")), nil, Verdict{Risk: RiskLow}},
		{"plain text update", riskEntity("a", body("New text")), liveOf("a", body("Old text")), Verdict{Risk: RiskLow}},
		{"title and alt only", riskEntity("a", map[string]any{"title": "T", "image": map[string]any{"alt": "a cat"}}), nil, Verdict{Risk: RiskLow}},
		{"relative link", riskEntity("a", body(`<a href="/about">about</a>`)), nil, Verdict{Risk: RiskLow}},
		{"known site host", riskEntity("a", body(`<a href="https://Example.org/x">x</a>`)), nil, Verdict{Risk: RiskLow}},
		{"mailto link", riskEntity("a", body(`<a href="mailto:a@b.c">m</a>`)), nil, Verdict{Risk: RiskLow}},
		{"new external host", riskEntity("a", body(`<a href="https://evil.test/x">x</a>`)), nil, Verdict{RiskHigh, ReasonLinkHost, 0}},
		{"external host in nested value", riskEntity("a", map[string]any{"blocks": []any{map[string]any{"text": `see <a href='//cdn.test/p'>p</a>`}}}), nil, Verdict{RiskHigh, ReasonLinkHost, 0}},
		{"markdown link", riskEntity("a", body("[x](https://evil.test/p)")), nil, Verdict{RiskHigh, ReasonLinkHost, 0}},
		{"bare url value", riskEntity("a", map[string]any{"cta": "https://evil.test"}), nil, Verdict{RiskHigh, ReasonLinkHost, 0}},
		{"javascript link", riskEntity("a", body(`<a href="javascript:alert(1)">x</a>`)), nil, Verdict{RiskHigh, ReasonLinkHost, 0}},
		{"host already linked live", riskEntity("a", body(`<a href="https://partner.test/a">b</a> more`)), liveOf("a", body(`<a href="https://partner.test/a">a</a>`)), Verdict{Risk: RiskLow}},
		{"link target changed on a known host", riskEntity("a", body(`<a href="https://partner.test/b">b</a>`)), liveOf("a", body(`<a href="https://partner.test/a">a</a>`)), Verdict{RiskHigh, ReasonLinkQuery, 0}},
		{"path and query both changed", riskEntity("a", body(`<a href="https://shop.test/q?aff=attacker">b</a>`)), liveOf("a", body(`<a href="https://shop.test/p?aff=owner">a</a>`)), Verdict{RiskHigh, ReasonLinkQuery, 0}},
		{"seo canonical to another site", seoEntity("a", &EntitySEO{Canonical: "https://evil.test/"}), liveOf("a", body("t")), Verdict{RiskHigh, ReasonLinkHost, 0}},
		{"seo canonical on a create", seoEntity("a", &EntitySEO{Canonical: "https://evil.test/"}), nil, Verdict{RiskHigh, ReasonLinkHost, 0}},
		{"seo canonical changed on a known host", seoEntity("a", &EntitySEO{Canonical: "https://partner.test/b"}), liveOf("a", map[string]any{"canonical_url": "https://partner.test/a"}), Verdict{RiskHigh, ReasonLinkQuery, 0}},
		{"seo canonical unchanged", seoEntity("a", &EntitySEO{Canonical: "https://partner.test/a"}), liveOf("a", map[string]any{"canonical_url": "https://partner.test/a"}), Verdict{Risk: RiskLow}},
		{"seo canonical on the site", seoEntity("a", &EntitySEO{Canonical: "https://example.org/a"}), nil, Verdict{Risk: RiskLow}},
		{"seo plain text", seoEntity("a", &EntitySEO{MetaTitle: "T", MetaDescription: "D", Robots: "noindex"}), nil, Verdict{Risk: RiskLow}},
		{"seo title with a bidi control", seoEntity("a", &EntitySEO{MetaTitle: "x\u202ey"}), nil, Verdict{RiskHigh, ReasonHiddenChar, 0}},
		{"seo description with a zero width space", seoEntity("a", &EntitySEO{MetaDescription: "x\u200by"}), nil, Verdict{RiskHigh, ReasonHiddenChar, 0}},
		{"link unchanged", riskEntity("a", body(`<a href="https://partner.test/a?ref=1">a</a> more`)), liveOf("a", body(`<a href="https://partner.test/a?ref=1">a</a>`)), Verdict{Risk: RiskLow}},
		{"query param changed", riskEntity("a", body(`<a href="https://partner.test/a?ref=2">a</a>`)), liveOf("a", body(`<a href="https://partner.test/a?ref=1">a</a>`)), Verdict{RiskHigh, ReasonLinkQuery, 0}},
		{"query param changed on site link", riskEntity("a", body(`<a href="/buy?aff=9">a</a>`)), liveOf("a", body(`<a href="/buy">a</a>`)), Verdict{RiskHigh, ReasonLinkQuery, 0}},
		{"iframe", riskEntity("a", body(`<p>x</p><iframe src="/f"></iframe>`)), nil, Verdict{RiskHigh, ReasonEmbed, 0}},
		{"embed tag", riskEntity("a", body(`<EMBED src="/f">`)), nil, Verdict{RiskHigh, ReasonEmbed, 0}},
		{"object tag", riskEntity("a", body(`<object data="/f"></object>`)), nil, Verdict{RiskHigh, ReasonEmbed, 0}},
		{"zero width space in a word", riskEntity("a", body("pass\u200bword")), nil, Verdict{RiskHigh, ReasonHiddenChar, 0}},
		{"word joiner", riskEntity("a", body("a\u2060b")), nil, Verdict{RiskHigh, ReasonHiddenChar, 0}},
		{"bom", riskEntity("a", body("a\ufeffb")), nil, Verdict{RiskHigh, ReasonHiddenChar, 0}},
		{"bidi override", riskEntity("a", body("safe \u202etxt.exe")), nil, Verdict{RiskHigh, ReasonHiddenChar, 0}},
		{"bidi isolate", riskEntity("a", map[string]any{"deep": []any{"x\u2067y"}}), nil, Verdict{RiskHigh, ReasonHiddenChar, 0}},
		{"slug change", riskEntity("b", body("t")), liveOf("a", body("t")), Verdict{RiskHigh, ReasonSlug, 0}},
		{"parent change", Item{Kind: KindEntity, Op: OpUpsert, Payload: rawJSON(map[string]any{"slug": "a", "parent_id": "p2", "attributes": body("t")})},
			&Live{Slug: "a", ParentID: "p1", Attributes: rawJSON(body("t"))}, Verdict{RiskHigh, ReasonSlug, 0}},
		{"same parent", Item{Kind: KindEntity, Op: OpUpsert, Payload: rawJSON(map[string]any{"slug": "a", "parent_id": "p1", "attributes": body("t")})},
			&Live{Slug: "a", ParentID: "p1", Attributes: rawJSON(body("t"))}, Verdict{Risk: RiskLow}},
		{"slug of a create is not a change", riskEntity("new", body("t")), nil, Verdict{Risk: RiskLow}},
		{"delete entity", Item{Kind: KindEntity, Op: OpDelete, TargetID: "x"}, nil, Verdict{RiskHigh, ReasonDelete, 0}},
		{"delete term", Item{Kind: KindTerm, Op: OpDelete, TargetID: "x"}, nil, Verdict{RiskHigh, ReasonDelete, 0}},
		{"attributes not an object", Item{Kind: KindEntity, Op: OpUpsert, Payload: json.RawMessage(`{"slug":"a","attributes":"<b>"}`)}, nil, Verdict{RiskHigh, ReasonUnparseable, 0}},
		{"payload not json", Item{Kind: KindEntity, Op: OpUpsert, Payload: json.RawMessage(`[1]`)}, nil, Verdict{RiskHigh, ReasonUnparseable, 0}},
		{"term", Item{Kind: KindTerm, Op: OpUpsert, Payload: rawJSON(map[string]any{"slug": "t", "name": "T"})}, nil, Verdict{Risk: RiskLow}},
		{"media alt change", Item{Kind: KindMedia, Op: OpUpsert, TargetID: "7f0c0b9e-1111-4222-8333-444455556666", Payload: rawJSON(map[string]any{"alt_text": "cat"})}, nil, Verdict{Risk: RiskHigh, Reason: ReasonAltText}},
		{"media", Item{Kind: KindMedia, Op: OpUpsert, Payload: rawJSON(map[string]any{"filename": "a.png", "alt_text": "cat"})}, nil, Verdict{Risk: RiskLow}},
		{"menu internal", Item{Kind: KindMenu, Op: OpUpsert, Payload: rawJSON(map[string]any{"items": []any{map[string]any{"Label": "A", "URL": "/a"}}})}, nil, Verdict{Risk: RiskLow}},
		{"menu external", Item{Kind: KindMenu, Op: OpUpsert, Payload: rawJSON(map[string]any{"items": []any{map[string]any{"Label": "A", "URL": "https://evil.test/"}}})}, nil, Verdict{RiskHigh, ReasonMenuLink, 0}},
		{"menu site host", Item{Kind: KindMenu, Op: OpUpsert, Payload: rawJSON(map[string]any{"items": []any{map[string]any{"Label": "A", "URL": "https://example.org/a"}}})}, nil, Verdict{Risk: RiskLow}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.it, tc.live, env); got != tc.want {
				t.Errorf("Classify = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestRiskProposal(t *testing.T) {
	for _, kind := range []string{"site_settings", "url_patterns", "home_page", "menu_location", "anything"} {
		if got := ClassifyProposal(kind); got != RiskCritical {
			t.Errorf("ClassifyProposal(%q) = %q, want critical", kind, got)
		}
	}
}

func TestRiskRedirectGroup(t *testing.T) {
	env := Env{StagedPaths: map[string]int{"/new-page": 4}}
	redirectTo := func(target string) Item {
		return Item{Kind: KindRedirect, Op: OpUpsert, Payload: rawJSON(map[string]any{"source_path": "/old", "target": target})}
	}
	tests := []struct {
		name   string
		target string
		want   Verdict
	}{
		{"target is a staged page", "/new-page", Verdict{RiskHigh, ReasonRedirect, 4}},
		{"target not staged", "/elsewhere", Verdict{RiskHigh, ReasonRedirect, 0}},
		{"external target", "https://evil.test/", Verdict{RiskHigh, ReasonRedirect, 0}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(redirectTo(tc.target), nil, env); got != tc.want {
				t.Errorf("Classify = %+v, want %+v", got, tc.want)
			}
		})
	}
}
