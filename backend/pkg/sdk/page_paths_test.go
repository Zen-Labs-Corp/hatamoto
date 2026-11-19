package sdk

import (
	jsonv2 "encoding/json/v2"
	"strings"
	"testing"
)

func pagePathsManifest(paths ...string) Manifest {
	return Manifest{Name: "pager", Version: "0.1.0", SDK: "^0.24", PagePaths: paths}
}

func TestPagePathsValid(t *testing.T) {
	m := pagePathsManifest("/", "/members", "/account", "/caf\u00e9")
	if err := m.Validate(); err != nil {
		t.Fatalf("valid page_paths refused: %v", err)
	}
}

func TestPagePathsTrailingSlashRefused(t *testing.T) {
	err := pagePathsManifest("/account/").Validate()
	if err == nil || !strings.Contains(err.Error(), `"/account/"`) || !strings.Contains(err.Error(), "trailing slash") {
		t.Errorf("err = %v, want a trailing slash refusal naming the path", err)
	}
}

func TestPagePathsCount(t *testing.T) {
	var paths []string
	for i := range MaxPagePaths {
		paths = append(paths, "/p"+strings.Repeat("a", i))
	}
	if err := pagePathsManifest(paths...).Validate(); err != nil {
		t.Fatalf("%d paths refused: %v", MaxPagePaths, err)
	}
	paths = append(paths, "/extra")
	if err := pagePathsManifest(paths...).Validate(); err == nil || !strings.Contains(err.Error(), "more than 16 page_paths") {
		t.Errorf("17 paths: err = %v", err)
	}
}

func TestPagePathsDuplicate(t *testing.T) {
	err := pagePathsManifest("/members", "/members").Validate()
	if err == nil || !strings.Contains(err.Error(), "duplicate page_paths") {
		t.Errorf("err = %v, want duplicate refusal", err)
	}
}

func TestPagePathsShape(t *testing.T) {
	for _, p := range []string{
		"members", "/a?b", "/a#b", "/a*", "/caf%C3%A9", "/a\\b", "//a",
		"/a/../b", "/a/./b", "/a b", "/" + strings.Repeat("a", 256),
	} {
		err := pagePathsManifest(p).Validate()
		if err == nil || !strings.Contains(err.Error(), "page_paths") {
			t.Errorf("page_paths %q: err = %v, want a page_paths refusal", p, err)
		}
	}
}

func TestPagePathsJSONKey(t *testing.T) {
	b, err := jsonv2.Marshal(pagePathsManifest("/members"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"page_paths":["/members"]`) {
		t.Errorf("marshalled manifest = %s, want the page_paths key", b)
	}
	var m Manifest
	if err := jsonv2.Unmarshal([]byte(`{"name":"p","version":"0.1.0","sdk":"^0.24","page_paths":["/account"]}`), &m); err != nil {
		t.Fatal(err)
	}
	if len(m.PagePaths) != 1 || m.PagePaths[0] != "/account" {
		t.Errorf("PagePaths = %v", m.PagePaths)
	}
}
