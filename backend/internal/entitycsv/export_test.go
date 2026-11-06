package entitycsv

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"flag"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
)

var update = flag.Bool("update", false, "rewrite golden files")

const exportSchema = `{"type":"object","properties":{
 "title":{"type":"string"},
 "price":{"type":"number"},
 "tags":{"type":"array","items":{"type":"string"}},
 "seo":{"type":"object","properties":{"title":{"type":"string"},"og":{"type":"object","properties":{"image":{"type":"string"}}}}},
 "page_doc":{"type":"object"}
}}`

func TestAttributeColumns(t *testing.T) {
	var ids []string
	for _, c := range AttributeColumns(jsontext.Value(exportSchema)) {
		ids = append(ids, c.ID)
	}
	want := []string{"attr:title", "attr:price", "attr:tags", "attr:seo.title", "attr:seo.og.image", "attr:page_doc"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("columns = %v, want %v", ids, want)
	}
	for _, c := range AttributeColumns(jsontext.Value(exportSchema)) {
		if c.Default == (c.ID == "attr:page_doc") {
			t.Errorf("%s default = %v", c.ID, c.Default)
		}
	}
	if len(AttributeColumns(nil)) != 0 || len(AttributeColumns(jsontext.Value(`{"type":"object"}`))) != 0 {
		t.Error("a schema without properties has no attribute columns")
	}
}

func TestPickColumns(t *testing.T) {
	cols, err := PickColumns(jsontext.Value(exportSchema), []string{"name", "attr:seo.title"})
	if err != nil || len(cols) != 2 || cols[1].ID != "attr:seo.title" {
		t.Fatalf("PickColumns = %v, %v", cols, err)
	}
	for _, bad := range [][]string{{"nope"}, {"attr:missing"}, {"name", "name"}} {
		if _, err := PickColumns(jsontext.Value(exportSchema), bad); err == nil {
			t.Errorf("PickColumns(%v) accepted", bad)
		}
	}
}

func TestFlatten(t *testing.T) {
	got := Flatten(jsontext.Value(`{"title":"T","price":12.50,"ok":true,"none":null,"tags":["a","b"],"nums":[1,2],
		"seo":{"title":"S","og":{"image":"i.png"}},"rows":[{"a":1}],"empty":[],"page_doc":{"x":[1]}}`))
	want := map[string]string{
		"title": "T", "price": "12.50", "ok": "true", "tags": "a|b", "nums": "1|2",
		"seo.title": "S", "seo.og.image": "i.png", "rows": `[{"a":1}]`, "empty": "", "page_doc": `{"x":[1]}`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Flatten = %v\nwant %v", got, want)
	}
}

func TestWriteCSVGolden(t *testing.T) {
	pub := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	parent := uuid.MustParse("00000000-0000-0000-0000-0000000000aa")
	mk := func(id, name, slug, key, attrs string) *entity.Entity {
		e := &entity.Entity{ID: uuid.MustParse(id), Name: name, Slug: slug, Status: entity.StatusPublished, Locale: "en",
			Attributes: jsontext.Value(attrs), PublishedAt: &pub, SortOrder: 3}
		if key != "" {
			e.Metadata = jsontext.Value(`{"import_key":"` + key + `"}`)
		}
		return e
	}
	a := mk("00000000-0000-0000-0000-000000000001", "=cmd|calc", "a", "csv:product:sku-1",
		`{"title":"Ünï ☃","price":-5,"tags":["x","y"],"seo":{"title":"S"}}`)
	a.ParentID = &parent
	b := mk("00000000-0000-0000-0000-000000000002", `Say "hi", ok`, "b", "wp:post:9", `{"title":"@home","tags":[],"seo":{"og":{"image":"+i"}}}`)
	b.Status, b.PublishedAt = entity.StatusDraft, nil
	pages := [][]*entity.Entity{{a}, {b}, nil}
	next := func() ([]*entity.Entity, error) { p := pages[0]; pages = pages[1:]; return p, nil }

	cols, err := PickColumns(jsontext.Value(exportSchema), []string{
		"key", "name", "slug", "status", "published_at", "locale", "parent_key", "order",
		"attr:title", "attr:price", "attr:tags", "attr:seo.title", "attr:seo.og.image"})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	err = WriteCSV(&buf, cols, next, ExportOptions{Type: "product", ParentKey: func(id uuid.UUID) string {
		if id == parent {
			return "root"
		}
		return ""
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(buf.String(), "\xEF\xBB\xBF") {
		t.Error("missing BOM")
	}
	const golden = "testdata/export.golden.csv"
	if *update {
		if err := os.WriteFile(golden, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("export differs from %s:\n%s", golden, buf.String())
	}
}

// Free-form objects, objects with a type list and objects deeper than the
// column depth export as one JSON cell and import back unchanged.
func TestFreeFormObjectsRoundTrip(t *testing.T) {
	schema := jsontext.Value(`{"type":"object","properties":{
		"title":{"type":"string"},
		"seo":{"type":"object"},
		"extra":{"type":["object","null"]},
		"loose":{},
		"a":{"type":"object","properties":{"b":{"type":"object","properties":{"c":{"type":"object","properties":{"d":{"type":"string"}}}}}}}}}`)
	attrs := `{"title":"T","seo":{"title":"S","og":{"image":"i.png"}},"extra":{"k":[1,2],"n":null},"loose":{"x":1},"a":{"b":{"c":{"d":"deep","e":true}}}}`
	e := &entity.Entity{ID: uuid.New(), Name: "N", Slug: "n", Attributes: jsontext.Value(attrs), Metadata: jsontext.Value(`{"import_key":"csv:product:k1"}`)}
	cols, err := PickColumns(schema, []string{"key", "name", "attr:title", "attr:seo", "attr:extra", "attr:loose", "attr:a.b.c"})
	if err != nil {
		t.Fatal(err)
	}
	pages := [][]*entity.Entity{{e}, nil}
	next := func() ([]*entity.Entity, error) { p := pages[0]; pages = pages[1:]; return p, nil }
	var buf bytes.Buffer
	if err := WriteCSV(&buf, cols, next, ExportOptions{Type: "product"}); err != nil {
		t.Fatal(err)
	}
	m := Mapping{Type: "product", Cols: map[string]string{}}
	for _, c := range cols {
		m.Cols[c.ID] = c.ID
	}
	imp := &fakeImporter{}
	sc := &entity.EntitySchema{ID: uuid.New(), Slug: "product", Schema: schema}
	rep, err := Run(context.Background(), imp, nil, sc, m, strings.NewReader(strings.TrimPrefix(buf.String(), "\xEF\xBB\xBF")), false)
	if err != nil || rep.Failed != 0 {
		t.Fatalf("re-import = %+v, %v\n%s", rep, err, buf.String())
	}
	var got, want any
	if err := json.Unmarshal(imp.batches[0].Items[0].Attributes, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(attrs), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip = %s\nwant %s\ncsv:\n%s", imp.batches[0].Items[0].Attributes, attrs, buf.String())
	}
}
