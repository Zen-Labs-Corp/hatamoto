package sdk

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestCommentPayloadKeys pins the exact top-level JSON keys of every comment
// payload. Adding a field later must fail here so privacy is reviewed first.
func TestCommentPayloadKeys(t *testing.T) {
	now := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	item := CommentItem{ID: "i", ParentID: "p", Depth: 1, AuthorName: "a", Staff: true, BodyHTML: "<p>b</p>", CreatedAt: now, CommenterKey: "k", Rank: 1.5, Status: "approved"}
	values := map[string]any{
		"CommentEvent":   CommentEvent{ID: "i", EntityID: "e", ParentID: "p", AuthorName: "a", Staff: true, BodyHTML: "b", Status: "approved", Locale: "en", CreatedAt: now, CommenterKey: "k"},
		"CommentCheck":   CommentCheck{Body: "b", LinkDomains: []string{"d"}, BodyHash: "h", Score: 1, Reasons: []string{"r"}, CommenterKey: "k", Schema: "story", AddrKey: "a", AreaKey: "n"},
		"CommentItem":    item,
		"CommentThread":  CommentThread{Items: []CommentItem{item}, Page: 1, HasMore: true},
		"CommentCounts":  CommentCounts{Counts: map[string]int{"e": 1}},
		"CommentSubtree": CommentSubtree{Ancestors: []CommentItem{item}, Items: []CommentItem{item}, Page: 1, HasMore: true},
		"CommentDraft":   CommentDraft{CommenterKey: "k", AuthorName: "a", Schema: "story", Reject: "r"},
	}
	want := map[string][]string{
		"CommentEvent":   {"author_name", "body_html", "commenter_key", "created_at", "entity_id", "id", "locale", "parent_id", "staff", "status"},
		"CommentCheck":   {"addr_key", "area_key", "body", "body_hash", "commenter_key", "link_domains", "reasons", "schema", "score"},
		"CommentItem":    {"author_name", "body_html", "commenter_key", "created_at", "depth", "id", "parent_id", "rank", "staff", "status"},
		"CommentThread":  {"has_more", "items", "page"},
		"CommentCounts":  {"counts"},
		"CommentSubtree": {"ancestors", "has_more", "items", "page"},
		"CommentDraft":   {"author_name", "commenter_key", "reject", "schema"},
	}
	banned := []string{"email", "ip", "user_agent", "net", "body_raw", "url", "website", "spam_"}
	for name, v := range values {
		raw, err := jsonv2.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]jsontext.Value
		if err := jsonv2.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		var got []string
		for k := range m {
			got = append(got, k)
		}
		slices.Sort(got)
		if !slices.Equal(got, want[name]) {
			t.Errorf("%s keys = %v, want %v", name, got, want[name])
		}
		for _, k := range got {
			bad := slices.Clone(banned)
			if name == "CommentCheck" {
				bad = append(bad, "entity")
			}
			for _, b := range bad {
				if strings.Contains(k, b) {
					t.Errorf("%s key %q contains %q", name, k, b)
				}
			}
		}
	}
}

// CommentCheckMaxScore is a public contract: plugins write against it.
func TestCommentCheckMaxScore(t *testing.T) {
	if CommentCheckMaxScore != 100 {
		t.Fatalf("CommentCheckMaxScore = %d, want 100", CommentCheckMaxScore)
	}
}
