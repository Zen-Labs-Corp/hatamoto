package render

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
)

// graphJSON is the site graph of v as indented JSON, the way the goldens store it.
func graphJSON(t *testing.T, v *PageView) []byte {
	t.Helper()
	b, err := json.MarshalIndent(siteGraphLD(v), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(b, '\n')
}

// compareGraphGolden compares got with testdata/golden/<name>.json, rewriting
// it under -update.
func compareGraphGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name+".json")
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from the site graph (run with -update after reviewing):\n%s", path, got)
	}
}

// TestSiteGraphGolden pins the full site graph over a neutral three-locale
// fixture: a locale with an office, one without, a shared page and a
// single-locale site.
func TestSiteGraphGolden(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "fixtures", "site-graph.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pages map[string]*PageView
	if err := json.Unmarshal(raw, &pages); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"xx", "yy", "shared"} {
		v, ok := pages[name]
		if !ok {
			t.Fatalf("fixture has no %q page", name)
		}
		t.Run(name, func(t *testing.T) {
			compareGraphGolden(t, "site-graph-"+name, graphJSON(t, v))
		})
	}
	t.Run("single", func(t *testing.T) {
		v := loadFixtureView(t, "head")
		compareGraphGolden(t, "site-graph-single", graphJSON(t, v))
	})
}

var graphScriptRx = regexp.MustCompile(`<script type="application/ld\+json">(\{"@context":"https://schema.org","@graph":.*?)</script>`)

// A single-locale site prints the same graph as the one pinned in head.html.
func TestSiteGraphSingleMatchesHeadGolden(t *testing.T) {
	head, err := os.ReadFile(filepath.Join("testdata", "golden", "head.html"))
	if err != nil {
		t.Fatal(err)
	}
	m := graphScriptRx.FindSubmatch(head)
	if m == nil {
		t.Fatal("no site graph script in head.html")
	}
	var fromHead, fromGolden any
	if err := json.Unmarshal(m[1], &fromHead); err != nil {
		t.Fatal(err)
	}
	g, err := os.ReadFile(filepath.Join("testdata", "golden", "site-graph-single.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(g, &fromGolden); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fromHead, fromGolden) {
		t.Errorf("site-graph-single.json differs from the graph in head.html")
	}
}
