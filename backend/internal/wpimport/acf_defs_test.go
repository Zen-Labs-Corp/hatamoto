package wpimport

import (
	_ "embed"
	"strings"
	"testing"
	"time"
)

//go:embed testdata/plain-blog.wxr.xml
var plainBlogWXR string

//go:embed testdata/acf-directory.wxr.xml
var acfDirectoryWXR string

//go:embed testdata/acf-flexible.wxr.xml
var acfFlexibleWXR string

//go:embed testdata/cf7-form.wxr.xml
var cf7FormWXR string

func parseFixtureWXR(t *testing.T, x string) *Dump {
	t.Helper()
	d, err := ParseWXR(strings.NewReader(x), WXROptions{})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func findACFField(fields []ACFField, name string) *ACFField {
	for i := range fields {
		if fields[i].Name == name {
			return &fields[i]
		}
	}
	return nil
}

func assertNoACFItems(t *testing.T, d *Dump) {
	t.Helper()
	for _, it := range d.Items {
		if strings.HasPrefix(it.Type, "acf-") || strings.HasPrefix(it.Type, "wpcf7") {
			t.Errorf("item %s of type %s must not be imported", it.ID, it.Type)
		}
	}
	for _, c := range d.Detect.CustomTypes {
		if strings.HasPrefix(c.Slug, "acf-") || strings.HasPrefix(c.Slug, "wpcf7") {
			t.Errorf("custom type %s must not be detected", c.Slug)
		}
	}
}

func TestACFDefinitionsDirectory(t *testing.T) {
	d := parseFixtureWXR(t, acfDirectoryWXR)
	assertNoACFItems(t, d)
	if len(d.ACF) != 1 {
		t.Fatalf("groups = %d, want 1", len(d.ACF))
	}
	g := d.ACF[0]
	if g.Title != "Listing" || g.Guessed || len(g.Types) != 1 || g.Types[0] != "listing" || len(g.OtherLocations) != 0 {
		t.Fatalf("group = %+v", g)
	}
	if len(g.Fields) != 11 {
		t.Fatalf("fields = %d, want 11", len(g.Fields))
	}
	staff := findACFField(g.Fields, "staff")
	if staff == nil || staff.Type != "repeater" || len(staff.Sub) != 3 {
		t.Fatalf("staff = %+v", staff)
	}
	size := findACFField(g.Fields, "size")
	if size == nil || len(size.Choices) != 3 || size.Choices[0].Value != "small" || size.Choices[0].Label != "Small" {
		t.Errorf("size = %+v", size)
	}
	if rel := findACFField(g.Fields, "related"); rel == nil || len(rel.PostTypes) != 1 || rel.PostTypes[0] != "listing" {
		t.Errorf("related = %+v", rel)
	}
	if m := findACFField(g.Fields, "map"); m == nil || m.Type != "google_map" {
		t.Errorf("map = %+v", m)
	}
	if len(d.Warnings) != 0 {
		t.Errorf("warnings = %v", d.Warnings)
	}
	if got := len(d.Items[0].Meta["about"]); got < 2048 {
		t.Errorf("long ACF value was cut to %d bytes", got)
	}
	if len(d.Detect.CustomTypes) != 1 || d.Detect.CustomTypes[0].Slug != "listing" {
		t.Errorf("custom types = %+v", d.Detect.CustomTypes)
	}
}

func TestACFLongValueOnlyForACFKeys(t *testing.T) {
	meta := map[string]string{"about": "x", "_about": "field_0123456789abc", "plain": "y", "staff": "1", "_staff": "field_0123456789abd", "staff_0_bio": "z"}
	long := strings.Repeat("a", 3000)
	for k, want := range map[string]bool{"about": true, "plain": false, "staff_0_bio": true} {
		if got := keepMeta(meta, k, long); got != want {
			t.Errorf("keepMeta(%s) = %v, want %v", k, got, want)
		}
	}
	if keepMeta(meta, "about", strings.Repeat("a", maxACFValue+1)) {
		t.Error("a value over 1 MiB was kept")
	}
}

func TestACFDefinitionsFlexible(t *testing.T) {
	d := parseFixtureWXR(t, acfFlexibleWXR)
	assertNoACFItems(t, d)
	if len(d.ACF) != 1 {
		t.Fatalf("groups = %d", len(d.ACF))
	}
	g := d.ACF[0]
	if len(g.Types) != 2 || g.Types[0] != "page" || g.Types[1] != "landing" {
		t.Errorf("types = %v", g.Types)
	}
	sections := findACFField(g.Fields, "sections")
	if sections == nil || sections.Type != "flexible_content" || len(sections.Layouts) != 3 {
		t.Fatalf("sections = %+v", sections)
	}
	hero, text, cols := sections.Layouts[0], sections.Layouts[1], sections.Layouts[2]
	if hero.Name != "hero" || len(hero.Sub) != 2 || hero.Sub[1].Type != "image" {
		t.Errorf("hero = %+v", hero)
	}
	if text.Name != "text_block" || len(text.Sub) != 1 || text.Sub[0].Type != "wysiwyg" {
		t.Errorf("text = %+v", text)
	}
	if cols.Name != "columns" || len(cols.Sub) != 1 || cols.Sub[0].Type != "flexible_content" || len(cols.Sub[0].Layouts) != 1 {
		t.Errorf("columns = %+v", cols)
	}
}

func TestACFInferenceWithoutDefinitions(t *testing.T) {
	// Drop the acf-* items, keeping the meta of the listings.
	var kept []string
	for _, chunk := range strings.Split(acfDirectoryWXR, " <item>") {
		if !strings.Contains(chunk, "<![CDATA[acf-") {
			kept = append(kept, chunk)
		}
	}
	d := parseFixtureWXR(t, strings.Join(kept, " <item>"))
	if len(d.ACF) != 1 || !d.ACF[0].Guessed {
		t.Fatalf("groups = %+v", d.ACF)
	}
	found := false
	for _, w := range d.Warnings {
		found = found || strings.Contains(w, "ACF field types guessed")
	}
	if !found {
		t.Errorf("warnings = %v", d.Warnings)
	}
	g := d.ACF[0]
	if len(g.Types) != 1 || g.Types[0] != "listing" {
		t.Errorf("types = %v", g.Types)
	}
	want := map[string]string{"staff": "repeater", "gallery": "gallery", "related": "relationship", "photo": "image",
		"opening_date": "date_picker", "price": "number", "phone": "text"}
	for name, typ := range want {
		if f := findACFField(g.Fields, name); f == nil || f.Type != typ {
			t.Errorf("%s = %+v, want type %s", name, f, typ)
		}
	}
	if staff := findACFField(g.Fields, "staff"); staff != nil {
		photo := findACFField(staff.Sub, "photo")
		if len(staff.Sub) != 3 || photo == nil || photo.Type != "image" {
			t.Errorf("staff sub = %+v", staff.Sub)
		}
	}
	if findACFField(g.Fields, "staff_0_name") != nil {
		t.Error("row keys must not become top-level fields")
	}
}

func TestACFInferenceFlexible(t *testing.T) {
	var kept []string
	for _, chunk := range strings.Split(acfFlexibleWXR, " <item>") {
		if !strings.Contains(chunk, "<![CDATA[acf-") {
			kept = append(kept, chunk)
		}
	}
	d := parseFixtureWXR(t, strings.Join(kept, " <item>"))
	if len(d.ACF) != 2 {
		t.Fatalf("groups = %d, want one per type", len(d.ACF))
	}
	s := findACFField(d.ACF[0].Fields, "sections")
	if s == nil || s.Type != "flexible_content" || len(s.Layouts) != 3 {
		t.Fatalf("sections = %+v", s)
	}
}

func TestACFOnPlainBlogAndCF7(t *testing.T) {
	d := parseFixtureWXR(t, plainBlogWXR)
	if d.ACF != nil || d.CF7 != nil || len(d.Items) != 3 || len(d.Warnings) != 0 {
		t.Errorf("plain blog: acf=%v cf7=%v items=%d warnings=%v", d.ACF, d.CF7, len(d.Items), d.Warnings)
	}
	d = parseFixtureWXR(t, cf7FormWXR)
	assertNoACFItems(t, d)
	if len(d.CF7) != 2 || len(d.Items) != 1 {
		t.Fatalf("cf7 = %d, items = %d", len(d.CF7), len(d.Items))
	}
	c := d.CF7[0]
	if c.Slug != "contact" || c.ID != "300" || c.Title != "Contact" || len(c.Hash) != 7 || !c.HasMail || !strings.Contains(c.Template, "[text* your-name]") {
		t.Errorf("contact = %+v", c)
	}
	if d.CF7[1].Slug != "quote-request" || d.CF7[1].Hash != "9f8e7d6" {
		t.Errorf("quote = %+v", d.CF7[1])
	}
	if d.ACF != nil {
		t.Errorf("acf = %v", d.ACF)
	}
}

func TestIsACFKey(t *testing.T) {
	meta := map[string]string{"a": "1", "_a": "field_0123456789abc", "a_0_b": "x", "other": "y", "_other": "not a key"}
	for k, want := range map[string]bool{"a": true, "a_0_b": true, "other": false, "missing": false} {
		if got := isACFKey(meta, k); got != want {
			t.Errorf("isACFKey(%s) = %v, want %v", k, got, want)
		}
	}
}

// A key made of many row segments must be answered at once: the check once
// recursed on every prefix and took exponential time.
func TestIsACFKeyIsFastOnHostileKeys(t *testing.T) {
	meta := map[string]string{"a": "1", "_a": "field_0123456789abc"}
	for _, k := range []string{strings.Repeat("b_1_", 60), "a" + strings.Repeat("_1_a", 60), strings.Repeat("a_1_", 62)} {
		start := time.Now()
		got := isACFKey(meta, k)
		if d := time.Since(start); d > time.Second {
			t.Fatalf("isACFKey(%d bytes) took %v", len(k), d)
		}
		if want := k[0] == 'a'; got != want && len(k) <= maxMetaKey {
			t.Errorf("isACFKey(%.20s...) = %v, want %v", k, got, want)
		}
	}
}
