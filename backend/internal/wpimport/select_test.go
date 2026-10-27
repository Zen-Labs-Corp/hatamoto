package wpimport

import "testing"

func TestSelectionApply(t *testing.T) {
	mk := func() *Dump {
		return &Dump{
			Items: []Item{
				{ID: "1", Type: "post", Status: wpPublish}, {ID: "2", Type: "post", Status: wpDraft}, {ID: "3", Type: "page", Status: wpPublish},
				{ID: "4", Type: "page", Status: wpPrivate}, {ID: "5", Type: "portfolio", Status: wpPublish}, {ID: "6", Type: "post", Status: wpPublish, Protected: true},
			},
			Menus:   []Menu{{ID: "m"}},
			OldURLs: []string{"/a"},
		}
	}
	ids := func(d *Dump) string {
		s := ""
		for _, it := range d.Items {
			s += it.ID
		}
		return s
	}
	d := mk()
	AllSelected().Apply(d)
	if ids(d) != "123456" || len(d.Menus) != 1 {
		t.Errorf("all: %s", ids(d))
	}
	d = mk()
	Selection{Types: []string{"post", "page"}, Drafts: false, Menus: false}.Apply(d)
	if ids(d) != "13" || len(d.Menus) != 0 || len(d.OldURLs) != 1 {
		t.Errorf("no drafts: %s menus=%d urls=%d", ids(d), len(d.Menus), len(d.OldURLs))
	}
	d = mk()
	Selection{Types: []string{"portfolio"}, Drafts: true, Menus: true}.Apply(d)
	if ids(d) != "5" {
		t.Errorf("one type: %s", ids(d))
	}
	d = mk()
	Selection{Types: []string{}, Drafts: true}.Apply(d)
	if ids(d) != "" {
		t.Errorf("no types: %s", ids(d))
	}
}
