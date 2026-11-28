package wpimport

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/media"
)

var (
	acfEntA = uuid.MustParse("00000000-0000-0000-0000-00000000000a")
	acfEntB = uuid.MustParse("00000000-0000-0000-0000-00000000000b")
)

// acfTestRunner is a Runner with a media registry and no database.
func acfTestRunner(t *testing.T, fields ...ACFField) (*Runner, *ACFGroup) {
	t.Helper()
	g := &ACFGroup{Title: "Listing", Types: []string{"listing"}}
	r := &Runner{
		res: &Result{ACF: &ACFResult{}}, media: newMediaRegistry(), dump: &Dump{}, hosts: map[string]bool{"old.example.test": true},
		infos: map[string]*entInfo{}, relIDs: map[string]uuid.UUID{}, schemaOf: map[string]string{"listing": "listing"},
		termPaths: map[string]string{}, mediaByID: map[string]Media{}, acf: map[string][]acfMapped{},
	}
	taken := map[string]bool{}
	for _, f := range fields {
		node, kind, lossy, _ := acfNode(f, false)
		r.acf["listing"] = append(r.acf["listing"], acfMapped{Prop: propName(f.Name, taken), Field: f, Group: g, Kind: kind, Node: node, Lossy: lossy})
		r.res.ACF.Fields = append(r.res.ACF.Fields, ACFFieldResult{Group: g.Title, Label: f.Label, Type: f.Type, Target: "listing", Status: "mapped"})
	}
	return r, g
}

func (r *Runner) addTestMedia(id, public string) {
	r.media.byID[id] = &media.Media{PublicURL: &public}
}

func acfJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := jsonx.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func directoryFields() []ACFField {
	return []ACFField{
		{Name: "price", Label: "Price", Type: "number"},
		{Name: "photo", Label: "Photo", Type: "image"},
		{Name: "staff", Label: "Staff", Type: "repeater", Sub: []ACFField{
			{Name: "name", Label: "Name", Type: "text"}, {Name: "photo", Label: "Photo", Type: "image"}}},
		{Name: "gallery", Label: "Gallery", Type: "gallery"},
		{Name: "opening_date", Label: "Opening", Type: "date_picker"},
		{Name: "size", Label: "Size", Type: "select", Choices: []ACFChoice{{"small", "Small"}, {"large", "Large"}}},
		{Name: "map", Label: "Map", Type: "google_map"},
		{Name: "about", Label: "About", Type: "textarea"},
		{Name: "website", Label: "Website", Type: "link"},
	}
}

func TestACFValuesDirectory(t *testing.T) {
	r, _ := acfTestRunner(t, directoryFields()...)
	for id, u := range map[string]string{"11": "/media/a.jpg", "12": "/media/b.jpg", "13": "/media/c.jpg", "14": "/media/d.jpg"} {
		r.addTestMedia(id, u)
	}
	about := strings.Repeat("long text ", 500)
	it := &Item{ID: "5", Type: "listing", Meta: map[string]string{
		"price": "12.5", "photo": "11", "staff": "3",
		"staff_0_name": "A", "staff_0_photo": "12", "staff_1_name": "B", "staff_2_name": "C", "staff_2_photo": "99",
		"gallery":      `a:2:{i:0;s:2:"13";i:1;s:2:"14";}`,
		"opening_date": "20260131", "size": "large", "map": `a:1:{s:7:"address";s:1:"x";}`,
		"about": about, "website": `a:2:{s:3:"url";s:19:"https://example.com";s:5:"title";s:1:"x";}`,
	}}
	attrs := r.acfAttrs(context.Background(), it, false)
	want := map[string]string{
		"price": "12.5", "photo": `"/media/a.jpg"`, "gallery": `["/media/c.jpg","/media/d.jpg"]`,
		"opening_date": `"2026-01-31"`, "size": `"large"`, "website": `"https://example.com"`,
		"staff": `[{"name":"A","photo":"/media/b.jpg"},{"name":"B"},{"name":"C"}]`,
	}
	for k, w := range want {
		if got := acfJSON(t, attrs[k]); got != w {
			t.Errorf("%s = %s, want %s", k, got, w)
		}
	}
	if s, _ := attrs["about"].(string); s != about {
		t.Errorf("about length %d, want %d", len(s), len(about))
	}
	if _, ok := attrs["map"].(string); !ok {
		t.Errorf("map = %v, want the raw string", attrs["map"])
	}
	if l := r.res.ACF.Fields[2]; l.Items != 1 || l.Rows != 3 {
		t.Errorf("staff line counts %+v", l)
	}
	for _, k := range attrs {
		if s, ok := k.(string); ok && IsWPContent(s) {
			t.Errorf("wp-content left in %q", s)
		}
	}
}

func TestACFValuesBadValuesKeepRaw(t *testing.T) {
	r, _ := acfTestRunner(t, directoryFields()...)
	r.opts.SkipMedia = true // an unknown wp-content file is not fetched
	it := &Item{ID: "6", Type: "listing", Meta: map[string]string{
		"opening_date": "soon", "price": "cheap", "size": "huge",
		"website": "https://old.example.test/wp-content/uploads/missing.pdf", "photo": "404"}}
	attrs := r.acfAttrs(context.Background(), it, false)
	for _, k := range []string{"opening_date_raw", "price_raw", "size_raw"} {
		if _, ok := attrs[k]; !ok {
			t.Errorf("no %s in %v", k, attrs)
		}
	}
	for _, k := range []string{"opening_date", "price", "size", "website", "photo", "website_raw"} {
		if _, ok := attrs[k]; ok {
			t.Errorf("%s should be absent, got %v", k, attrs[k])
		}
	}
	if !strings.Contains(strings.Join(r.res.Warnings, "\n"), "opening_date_raw") {
		t.Errorf("no warning for the raw date: %v", r.res.Warnings)
	}
}

func TestACFValuesDateTimeAndRichText(t *testing.T) {
	r, _ := acfTestRunner(t,
		ACFField{Name: "starts", Label: "Starts", Type: "date_time_picker"},
		ACFField{Name: "body", Label: "Body", Type: "wysiwyg"},
		ACFField{Name: "labels", Label: "Labels", Type: "checkbox"},
		ACFField{Name: "open", Label: "Open", Type: "true_false"})
	r.dump.Site.Timezone = "Etc/GMT-2"
	pub := "/media/img.jpg"
	r.media.addKey("2026/01/img.jpg", &media.Media{PublicURL: &pub})
	it := &Item{ID: "7", Type: "listing", Meta: map[string]string{
		"starts": "2026-03-01 10:00:00", "open": "0", "labels": `a:2:{i:0;s:1:"x";i:1;s:1:"y";}`,
		"body": `<p>Hi</p><p><img src="https://old.example.test/wp-content/uploads/2026/01/img.jpg" alt="x"></p>`}}
	attrs := r.acfAttrs(context.Background(), it, false)
	if got := acfJSON(t, attrs["starts"]); got != `"2026-03-01T10:00:00+02:00"` {
		t.Errorf("starts = %s", got)
	}
	if attrs["open"] != false || acfJSON(t, attrs["labels"]) != `["x","y"]` {
		t.Errorf("open/tags = %v %v", attrs["open"], attrs["labels"])
	}
	body, _ := attrs["body"].(string)
	if !strings.Contains(body, "/media/img.jpg") || IsWPContent(body) {
		t.Errorf("body = %s", body)
	}
}

func TestACFValuesFlexible(t *testing.T) {
	flex := ACFField{Name: "sections", Label: "Sections", Type: "flexible_content", Layouts: []ACFLayout{
		{Key: "layout_1", Name: "hero", Label: "Hero", Sub: []ACFField{{Name: "headline", Label: "Headline", Type: "text"}}},
		{Key: "layout_2", Name: "columns", Label: "Columns", Sub: []ACFField{
			{Name: "cols", Label: "Cols", Type: "repeater", Sub: []ACFField{{Name: "text", Label: "Text", Type: "text"}}}}},
	}}
	r, _ := acfTestRunner(t, flex)
	it := &Item{ID: "8", Type: "listing", Meta: map[string]string{
		"sections":            `a:2:{i:0;s:4:"hero";i:1;s:7:"columns";}`,
		"sections_0_headline": "Welcome",
		"sections_1_cols":     "2", "sections_1_cols_0_text": "one", "sections_1_cols_1_text": "two",
	}}
	attrs := r.acfAttrs(context.Background(), it, false)
	rows, _ := attrs["sections"].([]any)
	if len(rows) != 2 {
		t.Fatalf("rows = %v", attrs["sections"])
	}
	first, _ := rows[0].(map[string]any)
	second, _ := rows[1].(map[string]any)
	if first["layout"] != "hero" || first["headline"] != "Welcome" || second["layout"] != "columns" {
		t.Errorf("rows = %s", acfJSON(t, rows))
	}
	nested, _ := second["cols"].(string)
	if !strings.Contains(nested, "text: one") || !strings.Contains(nested, "text: two") {
		t.Errorf("nested = %q", nested)
	}
	if !r.acf["listing"][0].Lossy {
		t.Error("nested flexible content must be lossy")
	}
	if l := r.res.ACF.Fields[0]; l.Items != 1 || l.Rows != 2 {
		t.Errorf("counts %+v", l)
	}
}

func relationFields() []ACFField {
	return []ACFField{
		{Name: "related", Label: "Related", Type: "relationship", PostTypes: []string{"listing"}},
		{Name: "owner", Label: "Owner", Type: "post_object"},
		{Name: "price", Label: "Price", Type: "number"},
		{Name: "cards", Label: "Cards", Type: "repeater", Sub: []ACFField{
			{Name: "ref", Label: "Ref", Type: "post_object"}, {Name: "note", Label: "Note", Type: "text"}}},
	}
}

func TestACFRelationsDropUnresolvedIDsLikeTheLinksStep(t *testing.T) {
	r, _ := acfTestRunner(t, relationFields()...)
	r.infos["1"] = &entInfo{ent: &entity.Entity{ID: acfEntA}}
	meta := map[string]string{"related": `a:2:{i:0;s:1:"1";i:1;s:1:"2";}`, "price": "3"}
	it := &Item{ID: "9", Type: "listing", Meta: meta}
	content := r.acfAttrs(context.Background(), it, false)
	want := `["` + acfEntA.String() + `"]`
	if got := acfJSON(t, content["related"]); got != want {
		t.Errorf("related = %s, want %s", got, want)
	}
	if _, ok := content["price"]; !ok {
		t.Error("the plain field must still be written")
	}
	// The links step builds the same value, so a re-run changes nothing.
	links := map[string]any{}
	rd := r.newACFReader(context.Background(), it, true)
	for _, m := range r.acf["listing"] {
		if acfRelationBearing(m) {
			rd.field(m, links)
		}
	}
	if acfJSON(t, links["related"]) != acfJSON(t, content["related"]) {
		t.Errorf("links step %s differs from content step %s", acfJSON(t, links["related"]), acfJSON(t, content["related"]))
	}
	r.relIDs["2"] = acfEntB
	want = `["` + acfEntA.String() + `","` + acfEntB.String() + `"]`
	if got := acfJSON(t, r.acfAttrs(context.Background(), it, false)["related"]); got != want {
		t.Errorf("related = %s, want %s", got, want)
	}
}

func TestACFRelationsItemIsPartialAndExact(t *testing.T) {
	r, _ := acfTestRunner(t, relationFields()...)
	r.opts.Progress = func(string, string) {}
	r.infos["1"] = &entInfo{ent: &entity.Entity{ID: acfEntA}}
	item := &Item{ID: "9", Type: "listing", Meta: map[string]string{
		"related": `a:2:{i:0;s:1:"1";i:1;s:3:"404";}`, "price": "3",
		"cards": "1", "cards_0_ref": "1", "cards_0_note": "n"}}
	info := &entInfo{item: item, ent: &entity.Entity{ID: acfEntB, Name: "Nine"}}
	ii, ok := r.relationItem(context.Background(), info)
	if !ok {
		t.Fatal("no relation item")
	}
	if !ii.Partial || ii.TargetID == nil || *ii.TargetID != acfEntB || ii.SchemaSlug != "listing" || ii.Name != "Nine" {
		t.Errorf("item = %+v", ii)
	}
	if strings.Join(ii.ReplacePaths, ",") != "related,cards" {
		t.Errorf("replace paths = %v", ii.ReplacePaths)
	}
	var got map[string]any
	if err := jsonx.Unmarshal(ii.Attributes, &got, jsonx.StoredLoose); err != nil {
		t.Fatal(err)
	}
	if _, has := got["price"]; has || len(got) != 2 {
		t.Errorf("attributes carry more than the relationship props: %s", ii.Attributes)
	}
	if rel, _ := got["related"].([]any); len(rel) != 1 || rel[0] != acfEntA.String() {
		t.Errorf("related = %v: the unresolved id must be dropped", got["related"])
	}
	cards := acfJSON(t, got["cards"])
	if want := `[{"note":"n","ref":["` + acfEntA.String() + `"]}]`; cards != want {
		t.Errorf("cards = %s, want %s", cards, want)
	}
	if l := r.res.ACF.Fields[0]; l.Links != 1 {
		t.Errorf("links counted %d", l.Links)
	}
	if _, ok := r.relationItem(context.Background(), &entInfo{item: &Item{ID: "10", Type: "listing", Meta: map[string]string{"price": "1"}}, ent: &entity.Entity{ID: acfEntB}}); ok {
		t.Error("an item without relationship values needs no write")
	}
}

func TestACFValuesHostileInput(t *testing.T) {
	r, _ := acfTestRunner(t, directoryFields()...)
	huge := strings.Repeat("a:1:{", 100)
	it := &Item{ID: "1", Type: "listing", Meta: map[string]string{"staff": "999999", "gallery": huge, "website": huge, "size": huge, "photo": "../../x"}}
	_ = r.acfAttrs(context.Background(), it, false)
	if rows, _ := r.acfAttrs(context.Background(), it, false)["staff"].([]any); len(rows) > acfMaxRows {
		t.Errorf("%d rows kept", len(rows))
	}
}

// URL and email values the entity validator would refuse are kept as <name>_raw
// instead of sinking the item; code-running schemes are never stored as links.
func TestACFValuesRefusedByTheValidatorStayRaw(t *testing.T) {
	r, _ := acfTestRunner(t,
		ACFField{Name: "site", Label: "Site", Type: "url"},
		ACFField{Name: "mail", Label: "Mail", Type: "email"},
		ACFField{Name: "cta", Label: "CTA", Type: "link"},
		ACFField{Name: "alt", Label: "Alt", Type: "url"},
		ACFField{Name: "good", Label: "Good", Type: "url"},
		ACFField{Name: "ok", Label: "Ok", Type: "email"},
	)
	it := &Item{ID: "5", Type: "listing", Meta: map[string]string{
		"site": "#contact", "mail": "info@bücher.example", "cta": `a:1:{s:3:"url";s:17:"javascript:run(1)";}`,
		"alt": "//host.example.test/x", "good": "https://example.test/a", "ok": "info@example.test",
	}}
	attrs := r.acfAttrs(context.Background(), it, false)
	for _, k := range []string{"site", "mail", "cta", "alt"} {
		if _, ok := attrs[k]; ok {
			t.Errorf("%s = %v was stored as a typed value", k, attrs[k])
		}
	}
	if attrs["site_raw"] != "#contact" || attrs["mail_raw"] != "info@bücher.example" || attrs["alt_raw"] != "//host.example.test/x" {
		t.Errorf("raw fallbacks missing: %v", attrs)
	}
	if attrs["good"] != "https://example.test/a" || attrs["ok"] != "info@example.test" {
		t.Errorf("valid values lost: %v", attrs)
	}
	for _, v := range []string{"data:text/html,x", "JavaScript:run(1)", "vbscript:x"} {
		if acfLooksLikeURI(v) {
			t.Errorf("%q accepted", v)
		}
	}
}

// A warning quotes at most a short piece of a source value.
func TestACFWarningsCutLongSourceValues(t *testing.T) {
	r, _ := acfTestRunner(t, ACFField{Name: "photo", Label: "Photo", Type: "image"})
	it := &Item{ID: "5", Type: "listing", Meta: map[string]string{"photo": strings.Repeat("x", 1<<20)}}
	r.acfAttrs(context.Background(), it, false)
	if len(r.res.Warnings) == 0 {
		t.Fatal("no warning")
	}
	if n := len(r.res.Warnings[0]); n > 300 {
		t.Errorf("warning is %d bytes: %.120s", n, r.res.Warnings[0])
	}
}
