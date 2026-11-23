package wpimport

import (
	"strings"
	"testing"
)

func TestScanWPBakeryClassification(t *testing.T) {
	d := &Dump{
		RawBodies: true,
		Media:     []Media{{ID: "12", URL: "https://example.com/wp-content/uploads/a.jpg"}},
		Items: []Item{
			{ID: "1", Type: "page", Status: "publish", Title: "Layout only", Content: `[vc_row][vc_column][vc_custom_heading text="Title"][vc_single_image image="12"][vc_empty_space][/vc_column][/vc_row]`},
			{ID: "2", Type: "page", Status: "publish", Title: "With form", Content: `[vc_row][vc_column][vc_column_text]Text [contact-form-7 id="1"][/vc_column_text][/vc_column][/vc_row]`},
			{ID: "3", Type: "page", Status: "publish", Title: "With slider", Content: `[vc_row][vc_column][vc_posts_slider count="3"][/vc_column][/vc_row]`},
			{ID: "4", Type: "page", Status: "publish", Title: "Missing image", Content: `[vc_row][vc_column][vc_single_image image="99"][/vc_column][/vc_row]`},
			{ID: "5", Type: "page", Status: "publish", Title: "Class only", Content: `<div class="vc_row wpb_row">text</div>`},
			{ID: "6", Type: "page", Status: "publish", Title: "Plain", Content: "Just text."},
		},
	}
	r := Scan(d, "test")
	if f := r.Fidelity; f.High != 1 || f.Medium != 1 || f.Low != 4 || f.Percent != 1*100/6 {
		t.Errorf("Fidelity = %+v", f)
	}
	reason := map[string]string{}
	for _, n := range r.LowFidelity {
		reason[n.ID] = n.Reason
	}
	if reason["1"] != "WPBakery: layout not kept, content converted" {
		t.Errorf("layout-only reason = %q", reason["1"])
	}
	for id, wants := range map[string][]string{
		"2": {"WPBakery: layout not kept, content converted", "form removed: Contact Form 7"},
		"3": {"WPBakery: layout not kept, content converted", "shortcodes removed: vc_posts_slider"},
		"4": {"WPBakery: layout not kept, content converted", "images not found in the media library: 99"},
		"5": {"page builder (WPBakery): layout is not kept, review the text"},
	} {
		for _, want := range wants {
			if !strings.Contains(reason[id], want) {
				t.Errorf("item %s: reason %q lacks %q", id, reason[id], want)
			}
		}
	}
	if _, ok := reason["6"]; ok {
		t.Error("a plain page must not be listed")
	}
	if len(r.PageBuilders) != 1 || r.PageBuilders[0] != "WPBakery" {
		t.Errorf("PageBuilders = %v", r.PageBuilders)
	}
}
