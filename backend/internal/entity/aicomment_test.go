package entity

import (
	"strings"
	"testing"
)

func TestAICrawlerCommentPointsAtExistingFeeds(t *testing.T) {
	got := GenerateAICrawlerComment(AICommentSource{Slug: "platform", Name: "Platform"}, "Acme", "https://example.com")
	if got == "" {
		t.Fatal("empty comment")
	}
	if strings.Contains(got, "<script") {
		t.Errorf("comment must not embed a script tag (breaks JSON-LD scrapers):\n%s", got)
	}
	if strings.Contains(got, "/api/v1/public/page") {
		t.Errorf("comment advertises the deleted page endpoint:\n%s", got)
	}
	for _, want := range []string{"Schema.org JSON-LD", "For all entities: https://example.com/llms.txt", "-->"} {
		if !strings.Contains(got, want) {
			t.Errorf("comment misses %q:\n%s", want, got)
		}
	}
	if rel := GenerateAICrawlerComment(AICommentSource{Slug: "x", Name: "X"}, "Acme", ""); !strings.Contains(rel, "For all entities: /llms.txt") {
		t.Errorf("relative manifest URL missing:\n%s", rel)
	}
}
