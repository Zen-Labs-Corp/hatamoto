package llmstxt

import (
	"reflect"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 22, 14, 2, 0, 0, time.UTC)

func snapFor(mut func(*Site, []Section)) Snapshot {
	site := acmeSite()
	secs := []Section{
		{Kind: KindKey, Items: []Item{{Key: "en|home", Name: "Home", Path: "/", Desc: "Websites."}}},
		{Kind: KindPeople, Items: []Item{{Key: "en|elena", Name: "Elena Voss", Path: "/team/elena-voss", Job: "Lead designer"}}},
		{Kind: KindPosts, Items: []Item{{Key: "en|launch", Name: "Old launch post", Path: "/blog/launch", Desc: "Shipped."}}},
	}
	if mut != nil {
		mut(&site, secs)
	}
	return SnapshotOf(site, secs)
}

func texts(cs []Change) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Text)
	}
	return out
}

func TestDiff(t *testing.T) {
	base := snapFor(nil)
	cases := []struct {
		name     string
		cur      Snapshot
		excluded map[string]string
		want     []string
	}{
		{"no-op", snapFor(nil), nil, nil},
		{"added", snapFor(func(_ *Site, s []Section) {
			s[0].Items = append(s[0].Items, Item{Key: "en|pricing", Name: "Pricing", Path: "/pricing"})
		}), nil, []string{"Pricing: added"}},
		{"removed with reason", snapFor(func(_ *Site, s []Section) { s[2].Items = nil }),
			map[string]string{"en|launch": "opted out"}, []string{"Old launch post: removed (opted out)"}},
		{"removed plain", snapFor(func(_ *Site, s []Section) { s[2].Items = nil }), nil, []string{"Old launch post: removed"}},
		{"job title", snapFor(func(_ *Site, s []Section) { s[1].Items[0].Job = "Design lead" }), nil, []string{"Elena Voss: job title changed"}},
		{"rename", snapFor(func(_ *Site, s []Section) { s[0].Items[0].Name = "Start" }), nil, []string{"Start: title changed"}},
		{"address", snapFor(func(_ *Site, s []Section) { s[0].Items[0].Path = "/start" }), nil, []string{"Home: address changed"}},
		{"several fields", snapFor(func(_ *Site, s []Section) {
			s[1].Items[0].Job = "Design lead"
			s[1].Items[0].Desc = "Bio"
		}), nil, []string{"Elena Voss: job title and description changed"}},
		{"site items", snapFor(func(site *Site, _ []Section) { site.Summary = "New"; site.Name = "Acme"; site.Details = "x" }), nil,
			[]string{"Site details: changed", "Site name: changed", "Site summary: changed"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := texts(Diff(base, c.cur, c.excluded, t0))
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestDiffFirstBuild(t *testing.T) {
	if got := Diff(nil, snapFor(nil), nil, t0); len(got) != 0 {
		t.Fatalf("first build produced %v", got)
	}
	// An empty (not nil) snapshot is a real baseline: everything is new.
	if got := texts(Diff(Snapshot{}, snapFor(nil), nil, t0)); len(got) != 3 {
		t.Fatalf("empty baseline: %q", got)
	}
}

func TestKeep(t *testing.T) {
	older := []Change{{At: t0.Add(-2 * time.Hour), Text: "b"}, {At: t0.Add(-3 * time.Hour), Text: "c"}}
	fresh := []Change{{At: t0, Text: "a"}}
	got := texts(Keep(older, fresh, 2))
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("got %q", got)
	}
	if len(Keep(nil, nil, 20)) != 0 {
		t.Fatal("empty keep not empty")
	}
}
