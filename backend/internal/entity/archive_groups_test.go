package entity

import (
	"encoding/json/jsontext"
	"testing"
)

var testGroups = ArchiveGroupsSettings{
	Attribute: "sort", Divisor: 1000000, Path: "v{n}", Label: "v{n}.x",
	Title: "Item {n}.x notes", AnchorAttribute: "name", RedirectEntries: true,
}

func TestArchiveGroupBucket(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		attrs map[string]any
		want  int64
		ok    bool
	}{
		{"float", map[string]any{"sort": 1000008.0}, 1, true},
		{"string number", map[string]any{"sort": "2000001"}, 2, true},
		{"lower boundary", map[string]any{"sort": 1000000.0}, 1, true},
		{"below boundary", map[string]any{"sort": 999999.0}, 0, true},
		{"zero", map[string]any{"sort": 0.0}, 0, true},
		{"missing", map[string]any{}, 0, false},
		{"non numeric", map[string]any{"sort": "abc"}, 0, false},
		{"negative", map[string]any{"sort": -5.0}, 0, false},
		{"bool", map[string]any{"sort": true}, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			n, ok := testGroups.Bucket(tc.attrs)
			if n != tc.want || ok != tc.ok {
				t.Fatalf("Bucket = %d,%v want %d,%v", n, ok, tc.want, tc.ok)
			}
		})
	}
	if _, ok := (ArchiveGroupsSettings{}).Bucket(map[string]any{"sort": 1.0}); ok {
		t.Fatal("disabled settings must not bucket")
	}
}

func TestArchiveGroupURLParse(t *testing.T) {
	t.Parallel()
	for _, n := range []int64{0, 1, 12} {
		u := testGroups.URL("/items/", n)
		got, ok := testGroups.Parse("/items", u)
		if !ok || got != n {
			t.Fatalf("round trip %d via %q = %d,%v", n, u, got, ok)
		}
	}
	if u := testGroups.URL("/items", 1); u != "/items/v1" {
		t.Fatalf("URL = %q", u)
	}
	for _, p := range []string{"/items", "/items/v", "/items/vx", "/items/v01", "/items/v1/", "/items/v-1", "/other/v1", "/items/w1", "/items/v1.x"} {
		if n, ok := testGroups.Parse("/items", p); ok {
			t.Errorf("Parse(%q) = %d, want no match", p, n)
		}
	}
	suffixed := ArchiveGroupsSettings{Attribute: "a", Divisor: 1, Path: "p{n}-x"}
	if n, ok := suffixed.Parse("/i", "/i/p3-x"); !ok || n != 3 {
		t.Fatalf("suffixed = %d,%v", n, ok)
	}
	if _, ok := (ArchiveGroupsSettings{}).Parse("/i", "/i/v1"); ok {
		t.Fatal("disabled settings must not parse")
	}
}

func TestArchiveGroupHeading(t *testing.T) {
	t.Parallel()
	title, desc := ArchiveGroupsSettings{}.Heading(2, "Items")
	if title != "Items: 2" || desc != "" {
		t.Fatalf("defaults = %q %q", title, desc)
	}
	title, desc = testGroups.Heading(1, "Items")
	if title != "Item 1.x notes" || desc != "" {
		t.Fatalf("custom = %q %q", title, desc)
	}
	g := ArchiveGroupsSettings{Label: "v{n}.x", Title: "{archive_title} / {label}", Description: "All {n}.x of {archive_title}"}
	title, desc = g.Heading(3, "Items")
	if title != "Items / v3.x" || desc != "All 3.x of Items" {
		t.Fatalf("placeholders = %q %q", title, desc)
	}
	if l := testGroups.GroupLabel(4); l != "v4.x" {
		t.Fatalf("GroupLabel = %q", l)
	}
}

func TestArchiveGroupEntryTarget(t *testing.T) {
	t.Parallel()
	attrs := map[string]any{"sort": 1000008.0, "name": "v1.0.8"}
	if got := testGroups.EntryTarget("/items", attrs); got != "/items/v1#v1.0.8" {
		t.Fatalf("target = %q", got)
	}
	off := testGroups
	off.RedirectEntries = false
	if got := off.EntryTarget("/items", attrs); got != "" {
		t.Fatalf("redirect off = %q", got)
	}
	noAnchor := testGroups
	noAnchor.AnchorAttribute = ""
	if got := noAnchor.EntryTarget("/items", attrs); got != "/items/v1" {
		t.Fatalf("no anchor = %q", got)
	}
	if got := testGroups.EntryTarget("/items", map[string]any{"name": "x"}); got != "" {
		t.Fatalf("no bucket = %q", got)
	}
	if got := testGroups.EntryTarget("/items", map[string]any{"sort": 1.0}); got != "/items/v0" {
		t.Fatalf("missing anchor value = %q", got)
	}
}

func TestArchiveGroupSettingsDecodeLenient(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, in string
		limit    ArchiveLimitSetting
		enabled  bool
	}{
		{"valid", `{"archive_limit":10,"archive_groups":{"attribute":"sort","divisor":5,"path":"g{n}"}}`, 10, true},
		{"absent", `{}`, 0, false},
		{"garbage types", `{"archive_limit":"ten","archive_groups":"x"}`, 0, false},
		{"bad field type", `{"archive_limit":-3,"archive_groups":{"attribute":"sort","divisor":"5","path":"g{n}"}}`, 0, false},
		{"array", `{"archive_groups":[1]}`, 0, false},
		{"null", `{"archive_groups":null,"archive_limit":null}`, 0, false},
		{"path with slash disabled", `{"archive_groups":{"attribute":"sort","divisor":5,"path":"/g{n}"}}`, 0, false},
		{"bad anchor attribute disabled", `{"archive_groups":{"attribute":"sort","divisor":5,"path":"g{n}","anchor_attribute":"a b"}}`, 0, false},
		{"invalid path disabled", `{"archive_groups":{"attribute":"sort","divisor":5,"path":"g"}}`, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &EntitySchema{Settings: jsontext.Value(tc.in)}
			st, err := s.URLSettings()
			if err != nil {
				t.Fatalf("URLSettings must not fail: %v", err)
			}
			if st.ArchiveLimit != tc.limit || st.ArchiveGroups.Enabled() != tc.enabled {
				t.Fatalf("limit=%d enabled=%v want %d %v", st.ArchiveLimit, st.ArchiveGroups.Enabled(), tc.limit, tc.enabled)
			}
			if err := ValidateSchemaSettings(jsontext.Value(tc.in)); err != nil {
				t.Fatalf("load validation must not fail: %v", err)
			}
		})
	}
}
