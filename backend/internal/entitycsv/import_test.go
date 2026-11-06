package entitycsv

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
)

const importSchema = `{"type":"object","required":["title"],"properties":{
 "title":{"type":"string"},
 "price":{"type":"number"},
 "stock":{"type":"integer"},
 "live":{"type":"boolean"},
 "tags":{"type":"array","items":{"type":"string"}},
 "seo":{"type":"object","properties":{"title":{"type":"string"}}}
}}`

func schemaFixture() *entity.EntitySchema {
	return &entity.EntitySchema{ID: uuid.New(), Slug: "product", Schema: jsontext.Value(importSchema)}
}

// fakeImporter records batches; a row whose name is "boom" fails and a key it
// has seen before is an update.
type fakeImporter struct {
	batches []entity.ImportBatch
	seen    map[string]bool
}

func (f *fakeImporter) Import(_ context.Context, _ *uuid.UUID, b entity.ImportBatch) (*entity.ImportResult, error) {
	f.batches = append(f.batches, b)
	if f.seen == nil {
		f.seen = map[string]bool{}
	}
	res := &entity.ImportResult{Items: make([]entity.ImportItemResult, len(b.Items))}
	for i, it := range b.Items {
		r := &res.Items[i]
		r.Key = it.Key
		switch {
		case it.Name == "boom":
			r.Err = errors.New("slug already exists")
			res.Failed++
		case f.seen[it.Key]:
			res.Updated++
		default:
			r.Created = true
			res.Created++
			if !b.DryRun {
				f.seen[it.Key] = true
			}
		}
	}
	return res, nil
}

func stdMapping() Mapping {
	return Mapping{Type: "product", Cols: map[string]string{
		"sku": "key", "title": "name", "price": "attr:price", "tags": "attr:tags", "seo.title": "attr:seo.title",
		"state": "status", "when": "published_at", "up": "parent_key", "skip": "ignore",
	}}
}

func TestRun_RowsMappingAndErrors(t *testing.T) {
	csv := "\xEF\xBB\xBFsku,title,price,tags,seo.title,state,when,up,skip\n" +
		"a,Alpha,12.5,x|y,SEO,published,2026-01-02,,zzz\n" +
		"b,Beta,abc,,,,,a,\n" +
		"\n" +
		"c,boom,1,,,,,,\n" +
		",NoKey,1,,,,,,\n" +
		"d,Delta,1,,,archived?,,,\n" +
		"e,Echo,-5,,,,2026-13-45,,\n" +
		"a,Again,1,,,,,,\n" +
		"f,'=cmd,'-3,,,,,,\n"
	imp := &fakeImporter{}
	rep, err := Run(context.Background(), imp, nil, schemaFixture(), stdMapping(), strings.NewReader(csv), true)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != 8 || rep.Created != 2 || rep.Updated != 0 || rep.Failed != 6 {
		t.Fatalf("report = %+v", rep)
	}
	got := map[int]string{}
	for _, e := range rep.Errors {
		got[e.Row] = e.Column + ": " + e.Message
	}
	// Rows count records, so the blank line does not take a number.
	want := map[int]string{
		3: `price: "abc" is not a number`,
		4: ": slug already exists",
		5: "sku: the key is empty",
		6: `state: "archived?" is not a status (draft, scheduled, published or archived)`,
		7: `when: "2026-13-45" is not a date (use 2006-01-02 or 2006-01-02T15:04:05Z)`,
		8: `sku: key "a" is also on row 2`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("errors = %v\nwant %v", got, want)
	}
	if len(imp.batches) != 1 || !imp.batches[0].DryRun || imp.batches[0].Source != "csv" {
		t.Fatalf("batches = %+v", imp.batches)
	}
	items := imp.batches[0].Items
	// a, c (boom), f are sent; b has a bad number and is not.
	if len(items) != 3 {
		t.Fatalf("sent %d items: %+v", len(items), items)
	}
	a := items[0]
	if a.Key != "csv:product:a" || a.Name != "Alpha" || a.Status != entity.StatusPublished || a.SchemaSlug != "product" {
		t.Errorf("item a = %+v", a)
	}
	if a.PublishedAt.Format(time.DateOnly) != "2026-01-02" {
		t.Errorf("published_at = %v", a.PublishedAt)
	}
	if string(a.Attributes) != `{"price":12.5,"seo":{"title":"SEO"},"tags":["x","y"]}` {
		t.Errorf("attributes = %s", a.Attributes)
	}
	f := items[2]
	if f.Name != "=cmd" || string(f.Attributes) != `{"price":-3}` || f.Status != "" || f.NewStatus != entity.StatusDraft || !f.Partial {
		t.Errorf("item f = %+v (%s)", f, f.Attributes)
	}
}

func TestRun_BatchesStatusDefaultAndDryParents(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("sku,title,up\n")
	for i := range BatchSize + 5 {
		parent := ""
		if i == BatchSize+1 {
			parent = "0"
		}
		fmt.Fprintf(&sb, "%d,Row %d,%s\n", i, i, parent)
	}
	imp := &fakeImporter{}
	m := Mapping{Type: "product", Status: entity.StatusPublished, Cols: map[string]string{"sku": "key", "title": "name", "up": "parent_key"}}
	rep, err := Run(context.Background(), imp, nil, schemaFixture(), m, strings.NewReader(sb.String()), true)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Created != BatchSize+5 || len(imp.batches) != 2 || len(imp.batches[0].Items) != BatchSize {
		t.Fatalf("report %+v, %d batches", rep, len(imp.batches))
	}
	if it := imp.batches[0].Items[0]; it.Status != "" || it.NewStatus != entity.StatusPublished {
		t.Error("the default status must be sent as NewStatus only, so it cannot reach an existing row")
	}
	if p := imp.batches[1].Items[1].ParentKey; p != "" {
		t.Errorf("a dry-created parent of an earlier batch is still sent as %q", p)
	}

	// A real run keeps the parent key.
	imp = &fakeImporter{}
	if _, err := Run(context.Background(), imp, nil, schemaFixture(), m, strings.NewReader(sb.String()), false); err != nil {
		t.Fatal(err)
	}
	if p := imp.batches[1].Items[1].ParentKey; p != "csv:product:0" {
		t.Errorf("real run parent key = %q", p)
	}
	// Re-running updates.
	rep, _ = Run(context.Background(), imp, nil, schemaFixture(), m, strings.NewReader(sb.String()), false)
	if rep.Created != 0 || rep.Updated != BatchSize+5 {
		t.Fatalf("second run = %+v", rep)
	}
}

func TestRun_ErrorCapAndValidation(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("sku,title,price\n")
	for i := range MaxListedErrors + 20 {
		fmt.Fprintf(&sb, "%d,T,bad\n", i)
	}
	m := Mapping{Type: "product", Cols: map[string]string{"sku": "key", "title": "name", "price": "attr:price"}}
	rep, err := Run(context.Background(), &fakeImporter{}, nil, schemaFixture(), m, strings.NewReader(sb.String()), true)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Failed != MaxListedErrors+20 || len(rep.Errors) != MaxListedErrors {
		t.Fatalf("failed %d, listed %d", rep.Failed, len(rep.Errors))
	}
	bad := Mapping{Type: "product", Cols: map[string]string{"title": "name"}}
	if _, err := Run(context.Background(), &fakeImporter{}, nil, schemaFixture(), bad, strings.NewReader("title\nx\n"), true); err == nil {
		t.Error("a mapping without Key was accepted")
	}
}

func TestMappingValidate(t *testing.T) {
	attrs := AttributeColumns(jsontext.Value(importSchema))
	header := []string{"a", "b", "c"}
	ok := Mapping{Cols: map[string]string{"a": "key", "b": "name", "c": "attr:price"}}
	if err := ok.Validate(header, attrs); err != nil {
		t.Fatal(err)
	}
	for name, m := range map[string]Mapping{
		"no key":      {Cols: map[string]string{"b": "name"}},
		"no name":     {Cols: map[string]string{"a": "key"}},
		"dup target":  {Cols: map[string]string{"a": "key", "b": "name", "c": "name"}},
		"unknown":     {Cols: map[string]string{"a": "key", "b": "name", "c": "attr:nope"}},
		"order":       {Cols: map[string]string{"a": "key", "b": "name", "c": "order"}},
		"not in file": {Cols: map[string]string{"a": "key", "b": "name", "z": "slug"}},
		"bad status":  {Cols: ok.Cols, Status: "weird"},
	} {
		if err := m.Validate(header, attrs); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestSuggest(t *testing.T) {
	attrs := AttributeColumns(jsontext.Value(importSchema))
	got := Suggest([]string{"Key", "Name", "attr:price", "stock", "Parent key", "order", "id", "extra", "slug", "Slug"}, attrs)
	want := map[string]string{"Key": "key", "Name": "name", "attr:price": "attr:price", "stock": "attr:stock",
		"Parent key": "parent_key", "order": "ignore", "id": "ignore", "extra": "ignore", "slug": "slug", "Slug": "ignore"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Suggest = %v", got)
	}
}

func TestUnflatten(t *testing.T) {
	sc := jsontext.Value(importSchema)
	got, err := Unflatten(sc, map[string]string{"stock": "7", "live": "Yes", "tags": "a|b", "extra": "x", "seo.title": "S"})
	if err != nil || string(got) != `{"extra":"x","live":true,"seo":{"title":"S"},"stock":7,"tags":["a","b"]}` {
		t.Fatalf("Unflatten = %s, %v", got, err)
	}
	for cell, v := range map[string]string{"price": "nan", "stock": "1.5", "live": "maybe"} {
		_, err := Unflatten(sc, map[string]string{cell: v})
		var ce *CellError
		if !errors.As(err, &ce) || ce.Path != cell {
			t.Errorf("%s=%q: err = %v", cell, v, err)
		}
	}
}

func TestPreview(t *testing.T) {
	h, sample, rows, err := Preview(strings.NewReader("\xEF\xBB\xBFa,b\n1,2\n\n3,4\n5,6\n7,8\n"))
	if err != nil || !reflect.DeepEqual(h, []string{"a", "b"}) || rows != 4 || len(sample) != 3 {
		t.Fatalf("Preview = %v %v %d %v", h, sample, rows, err)
	}
	for name, in := range map[string]string{
		"duplicate": "a,a\n1,2\n", "empty header": "a,\n1,2\n", "no rows": "a,b\n", "empty": "", "bad quote": "a,b\n1,\"x\n",
	} {
		if _, _, _, err := Preview(strings.NewReader(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	var sb strings.Builder
	sb.WriteString("a\n")
	for range MaxRows + 1 {
		sb.WriteString("x\n")
	}
	if _, _, _, err := Preview(strings.NewReader(sb.String())); !errors.Is(err, ErrTooManyRows) {
		t.Errorf("too many rows: %v", err)
	}
}

func TestSpool(t *testing.T) {
	s := &Spool{Dir: filepath.Join(t.TempDir(), "spool")}
	id, err := s.Save(strings.NewReader("a,b\n1,2\n"), 100)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(s.Dir, "csv", id.String()+".csv"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("spool file: %v %v", fi, err)
	}
	if _, err := s.Save(strings.NewReader(""), 100); !errors.Is(err, ErrFileEmpty) {
		t.Errorf("empty: %v", err)
	}
	if _, err := s.Save(strings.NewReader(strings.Repeat("x", 101)), 100); !errors.Is(err, ErrFileTooLarge) {
		t.Errorf("large: %v", err)
	}
	st := &State{Type: "product", FileName: "p.csv", Header: []string{"a"}, Rows: 1, Mapping: &Mapping{Type: "product", Cols: map[string]string{"a": "key"}}, Dry: &Report{Created: 1}}
	if err := s.SaveState(id, st); err != nil {
		t.Fatal(err)
	}
	back, err := s.LoadState(id)
	if err != nil || !reflect.DeepEqual(back, st) {
		t.Fatalf("state = %+v, %v", back, err)
	}
	if _, err := s.LoadState(uuid.New()); !errors.Is(err, ErrNoUpload) {
		t.Errorf("missing state: %v", err)
	}
	// Old uploads go on the next save.
	old := time.Now().Add(-2 * SpoolRetention)
	for _, ext := range []string{".csv", ".json"} {
		_ = os.Chtimes(filepath.Join(s.Dir, "csv", id.String()+ext), old, old)
	}
	if _, err := s.Save(strings.NewReader("x\n"), 100); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(id); !errors.Is(err, ErrNoUpload) {
		t.Errorf("old upload kept: %v", err)
	}

	// Sweep alone (a wizard request without an upload) removes them too.
	id2, err := s.Save(strings.NewReader("y\n"), 100)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(filepath.Join(s.Dir, "csv", id2.String()+".csv"), old, old)
	s.Sweep()
	if _, err := s.Open(id2); !errors.Is(err, ErrNoUpload) {
		t.Errorf("Sweep kept an old upload: %v", err)
	}
}
