package sdk

import (
	jsonv2 "encoding/json/v2"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func contractManifest() Manifest {
	return Manifest{Name: "pager", Version: "0.1.0", SDK: "^0.27"}
}

func nKeys(n int, format string) []string {
	var out []string
	for i := range n {
		out = append(out, fmt.Sprintf(format, i))
	}
	return out
}

func TestManifestPageQuery(t *testing.T) {
	ok := [][]string{nil, {"p"}, nKeys(16, "k%d"), {"a-b_C9"}}
	for _, q := range ok {
		m := contractManifest()
		m.PageQuery = q
		if err := m.Validate(); err != nil {
			t.Errorf("page_query %v refused: %v", q, err)
		}
	}
	bad := [][]string{nKeys(17, "k%d"), {"a b"}, {""}, {strings.Repeat("a", 65)}, {"a", "a"}}
	for _, q := range bad {
		m := contractManifest()
		m.PageQuery = q
		if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "page_query") {
			t.Errorf("page_query %v: err = %v, want a page_query refusal", q, err)
		}
	}
}

func TestManifestPagePathPrefixes(t *testing.T) {
	for _, p := range [][]string{{"/item/"}, {"/a/", "/b/", "/c/", "/d/", "/e/", "/f/", "/g/", "/h/"}, {"/a/b/"}} {
		m := contractManifest()
		m.PagePathPrefixes = p
		if err := m.Validate(); err != nil {
			t.Errorf("page_path_prefixes %v refused: %v", p, err)
		}
	}
	bad := [][]string{
		{"/"}, {"/item"}, {"item/"}, {"/a//b/"}, {"/a?/"}, {"/a%20/"}, {"/../"},
		{"/a/", "/b/", "/c/", "/d/", "/e/", "/f/", "/g/", "/h/", "/i/"},
		{"/item/", "/item/"}, {""},
	}
	for _, p := range bad {
		m := contractManifest()
		m.PagePathPrefixes = p
		if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "page_path_prefixes") {
			t.Errorf("page_path_prefixes %v: err = %v, want a refusal", p, err)
		}
	}
}

func entityManifest(mut func(*Manifest)) Manifest {
	m := contractManifest()
	m.Permissions = []Permission{PermEntitiesWriteOwn, PermCommentsModerate}
	m.EntitySchema = "story"
	m.EntityFields = []string{"url", "text", "kind"}
	mut(&m)
	return m
}

func TestManifestEntitySchema(t *testing.T) {
	if err := entityManifest(func(*Manifest) {}).Validate(); err != nil {
		t.Fatalf("valid entity manifest refused: %v", err)
	}
	cases := map[string]func(*Manifest){
		"fields without schema": func(m *Manifest) {
			m.EntitySchema = ""
			m.Permissions = nil
		},
		"write without schema": func(m *Manifest) {
			m.EntitySchema = ""
			m.EntityFields = nil
			m.Permissions = []Permission{PermEntitiesWriteOwn}
		},
		"moderate without schema": func(m *Manifest) {
			m.EntitySchema = ""
			m.EntityFields = nil
			m.Permissions = []Permission{PermCommentsModerate}
		},
		"schema without write": func(m *Manifest) { m.Permissions = []Permission{PermCommentsModerate} },
		"underscore":           func(m *Manifest) { m.EntitySchema = "a_b" },
		"upper":                func(m *Manifest) { m.EntitySchema = "Story" },
		"leading dash":         func(m *Manifest) { m.EntitySchema = "-a" },
		"trailing dash":        func(m *Manifest) { m.EntitySchema = "a-" },
		"double dash":          func(m *Manifest) { m.EntitySchema = "a--b" },
		"41 bytes":             func(m *Manifest) { m.EntitySchema = strings.Repeat("a", 41) },
		"17 fields":            func(m *Manifest) { m.EntityFields = nKeys(17, "f%d") },
		"upper field":          func(m *Manifest) { m.EntityFields = []string{"Url"} },
		"digit field":          func(m *Manifest) { m.EntityFields = []string{"1a"} },
		"duplicate field":      func(m *Manifest) { m.EntityFields = []string{"url", "url"} },
	}
	for name, mut := range cases {
		if err := entityManifest(mut).Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	long := entityManifest(func(m *Manifest) { m.EntitySchema = strings.Repeat("a", 40); m.EntityFields = nKeys(16, "f%d") })
	if err := long.Validate(); err != nil {
		t.Errorf("40-byte schema and 16 fields refused: %v", err)
	}
}

func TestManifestAskedContractPermissions(t *testing.T) {
	m := entityManifest(func(m *Manifest) { m.Permissions = append(m.Permissions, PermClientKey) })
	asked := m.Asked()
	for _, p := range []Permission{PermClientKey, PermEntitiesWriteOwn, PermCommentsModerate} {
		if !slices.Contains(asked, p) {
			t.Errorf("Asked() = %v, missing %q", asked, p)
		}
	}
}

func TestManifestContractJSONKeys(t *testing.T) {
	m := entityManifest(func(m *Manifest) {
		m.PageQuery = []string{"p"}
		m.PagePathPrefixes = []string{"/item/"}
	})
	b, err := jsonv2.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"page_query":["p"]`, `"page_path_prefixes":["/item/"]`, `"entity_schema":"story"`, `"entity_fields":["url","text","kind"]`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("marshalled manifest = %s, want %s", b, key)
		}
	}
}
