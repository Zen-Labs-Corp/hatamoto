package importjob

import (
	"testing"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/wpimport"
)

// Options.Types is the one field where nil and empty differ: nil imports all
// post types, an empty list imports none. A stored options column must keep
// that distinction when it is read back.

func selection(o Options) wpimport.Selection {
	return wpimport.Selection{Types: o.Types, Drafts: o.Drafts, Menus: o.Menus}
}

func TestOptionsTypesNilMeansAll(t *testing.T) {
	if DefaultOptions().Types != nil {
		t.Fatal("DefaultOptions must select every type (nil Types)")
	}
	dump := func() *wpimport.Dump {
		return &wpimport.Dump{Items: []wpimport.Item{{Type: "post", Status: "publish"}, {Type: "page", Status: "publish"}}}
	}
	for _, c := range []struct {
		name    string
		stored  string
		wantNil bool
		kept    int
	}{
		{"absent", `{"drafts":true,"media":true,"menus":true}`, true, 2},
		{"null", `{"types":null,"drafts":true,"media":true,"menus":true}`, true, 2},
		{"empty list", `{"types":[],"drafts":true,"media":true,"menus":true}`, false, 0},
		{"one type", `{"types":["page"],"drafts":true,"media":true,"menus":true}`, false, 1},
		{"legacy without no_seo_live", `{"types":null,"drafts":true,"media":true,"menus":true,"keep_patterns":false,"keep_edited":false}`, true, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			var o Options
			if err := jsonx.Unmarshal([]byte(c.stored), &o, jsonx.StoredLoose); err != nil {
				t.Fatal(err)
			}
			if (o.Types == nil) != c.wantNil {
				t.Fatalf("Types = %#v, nil wanted: %v", o.Types, c.wantNil)
			}
			if o.NoSEOLive {
				t.Fatal("stored options without no_seo_live must keep the live read on")
			}
			d := dump()
			selection(o).Apply(d)
			if len(d.Items) != c.kept {
				t.Fatalf("kept %d items, want %d", len(d.Items), c.kept)
			}
		})
	}
}
