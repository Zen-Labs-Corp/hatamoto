package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/render"
)

type fakeConvertStore struct {
	rows    []convertRow
	updates []*entity.UpdateEntityInput
	// bump is added to every row's version when it is re-read before a write,
	// as if an editor saved after the candidate list was read.
	bump int
	// touch is added to every row's updated_at when re-read: a raw-SQL writer
	// (link rewrite) that changes attributes without bumping version.
	touch time.Duration
}

func (f *fakeConvertStore) Stamp(_ context.Context, id uuid.UUID) (int, time.Time, error) {
	for _, r := range f.rows {
		if r.ID == id {
			return r.Version + f.bump, r.UpdatedAt.Add(f.touch), nil
		}
	}
	return 0, time.Time{}, nil
}

func (f *fakeConvertStore) Candidates(context.Context, string) ([]convertRow, error) {
	return f.rows, nil
}

func (f *fakeConvertStore) Update(_ context.Context, _ uuid.UUID, in *entity.UpdateEntityInput) error {
	f.updates = append(f.updates, in)
	return nil
}

// diffRenderer renders a Doc view differently from a Sections view.
type diffRenderer struct{}

func (diffRenderer) RenderPage(w io.Writer, v *render.PageView) error {
	if v.Doc != nil {
		_, err := io.WriteString(w, "doc")
		return err
	}
	_, err := io.WriteString(w, "sections")
	return err
}

func newConvertEngine(t *testing.T) *render.Engine {
	t.Helper()
	eng, err := render.New(render.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return eng
}

const convertSections = `{"page_sections":[
 {"id":"b","type":"content","sort_order":2,"content":{"body":"<p>second</p>"}},
 {"id":"a","type":"hero","title":"T","sort_order":1,"content":{"headline":"First"}},
 {"id":"c","type":"content","isVisible":false,"sort_order":3,"content":{"body":"<p>hidden</p>"}}]}`

func rowWith(attrs string) convertRow {
	return convertRow{ID: uuid.New(), Name: "P", Slug: "p", Attrs: json.RawMessage(attrs)}
}

func TestConvertPagesWritesDocThroughService(t *testing.T) {
	st := &fakeConvertStore{rows: []convertRow{rowWith(convertSections)}}
	var out bytes.Buffer
	if err := convertPages(context.Background(), st, newConvertEngine(t), convertOpts{}, &out); err != nil {
		t.Fatalf("err = %v\n%s", err, out.String())
	}
	if len(st.updates) != 1 || *st.updates[0].ChangeSummary != "Converted to builder doc" {
		t.Fatalf("updates = %+v", st.updates)
	}
	if !st.updates[0].KeepUpdatedAt {
		t.Error("convert must keep updated_at")
	}
	var attrs map[string]any
	if err := json.Unmarshal(*st.updates[0].Attributes, &attrs); err != nil {
		t.Fatal(err)
	}
	if _, ok := attrs["page_doc"].(map[string]any); !ok || attrs["page_sections"] == nil {
		t.Errorf("attrs = %v", attrs)
	}
	if !strings.Contains(out.String(), "pages: 1 convertible: 1 render-identical: 1 diffs: 0 skipped (already v2): 0") {
		t.Errorf("out = %s", out.String())
	}
}

func TestConvertPagesDryRunWritesNothing(t *testing.T) {
	st := &fakeConvertStore{rows: []convertRow{rowWith(convertSections)}}
	var out bytes.Buffer
	err := convertPages(context.Background(), st, newConvertEngine(t), convertOpts{DryRun: true, VerifyRender: true}, &out)
	if err != nil || len(st.updates) != 0 {
		t.Fatalf("err=%v updates=%d", err, len(st.updates))
	}
	if !strings.Contains(out.String(), "render-identical: 1 diffs: 0") {
		t.Errorf("out = %s", out.String())
	}
}

func TestConvertPagesSkipsAlreadyV2(t *testing.T) {
	attrs := `{"page_sections":[],"page_doc":{"v":2,"nodes":[]}}`
	st := &fakeConvertStore{rows: []convertRow{rowWith(attrs)}}
	var out bytes.Buffer
	if err := convertPages(context.Background(), st, newConvertEngine(t), convertOpts{}, &out); err != nil {
		t.Fatal(err)
	}
	if len(st.updates) != 0 || !strings.Contains(out.String(), "skipped (already v2): 1") {
		t.Errorf("updates=%d out=%s", len(st.updates), out.String())
	}
}

func TestConvertPagesDiffFailsAndWritesNothing(t *testing.T) {
	st := &fakeConvertStore{rows: []convertRow{rowWith(convertSections)}}
	var out bytes.Buffer
	err := convertPages(context.Background(), st, diffRenderer{}, convertOpts{}, &out)
	if err == nil {
		t.Fatal("a render diff must be an error (exit non-zero)")
	}
	if len(st.updates) != 0 || !strings.Contains(out.String(), "DIFF") || !strings.Contains(out.String(), "diffs: 1") {
		t.Errorf("updates=%d out=%s", len(st.updates), out.String())
	}
}

func TestBuilderConvertRevertUnsupported(t *testing.T) {
	err := runBuilderConvert(context.Background(), "", []string{"--revert"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "unsupported: the page_sections mirror is no longer maintained") {
		t.Fatalf("err = %v", err)
	}
}

// Content stored before today's sanitiser policy must survive a conversion
// byte for byte, in both trees (an unchanged value is never re-sanitised).
func TestConvertPagesKeepsLegacyContentByteIdentical(t *testing.T) {
	const body = `<div onclick=\"x()\">hi</div><script>track()</script>`
	attrs := `{"page_sections":[{"id":"h","type":"html","sort_order":0,"content":{"body":"` + body + `","content":"<p onclick=\"y()\">rich</p>"}}]}`
	st := &fakeConvertStore{rows: []convertRow{rowWith(attrs)}}
	var out bytes.Buffer
	if err := convertPages(context.Background(), st, newConvertEngine(t), convertOpts{}, &out); err != nil {
		t.Fatalf("err = %v\n%s", err, out.String())
	}
	if len(st.updates) != 1 {
		t.Fatalf("updates = %d\n%s", len(st.updates), out.String())
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	var decoded any
	if err := json.Unmarshal(*st.updates[0].Attributes, &decoded); err != nil {
		t.Fatal(err)
	}
	_ = enc.Encode(decoded)
	got := buf.String()
	if n := strings.Count(got, body); n != 2 {
		t.Errorf("unsanitised body present %d times, want 2 (doc + mirror): %s", n, got)
	}
	if n := strings.Count(got, `<p onclick=\"y()\">rich</p>`); n != 2 {
		t.Errorf("rich content rewritten (%d copies): %s", n, got)
	}
}

// A string sort_order is honoured by the public renderer; convert must read
// it the same way, keep unknown section keys and keep numbers digit for digit.
func TestConvertPagesSectionParityAndExactValues(t *testing.T) {
	attrs := `{"page_sections":[` +
		`{"id":"a","type":"content","sort_order":"2","anchor":"top","content":{"body":"<p>a</p>","big":12345678901234567890,"price":9.90}},` +
		`{"id":"b","type":"content","sort_order":"1","content":{"body":"<p>b</p>"}}]}`
	st := &fakeConvertStore{rows: []convertRow{rowWith(attrs)}}
	var out bytes.Buffer
	if err := convertPages(context.Background(), st, newConvertEngine(t), convertOpts{}, &out); err != nil {
		t.Fatalf("err = %v\n%s", err, out.String())
	}
	got := string(*st.updates[0].Attributes)
	for _, want := range []string{`"anchor":"top"`, `12345678901234567890`, `9.90`} {
		if strings.Count(got, want) != 2 {
			t.Errorf("%s: want 2 copies (doc + the frozen mirror) in %s", want, got)
		}
	}
	var m struct {
		Doc struct {
			Nodes []struct {
				C []struct {
					ID string `json:"id"`
				} `json:"c"`
			} `json:"nodes"`
		} `json:"page_doc"`
	}
	if err := json.Unmarshal([]byte(got), &m); err != nil || len(m.Doc.Nodes) != 2 || m.Doc.Nodes[0].C[0].ID != "b" {
		t.Errorf("the doc must follow the string sort_order (b first): %s", got)
	}
}

// A page an editor saved after the candidate read is not overwritten.
func TestConvertPagesSkipsPageChangedSinceRead(t *testing.T) {
	st := &fakeConvertStore{rows: []convertRow{rowWith(convertSections)}, bump: 1}
	var out bytes.Buffer
	err := convertPages(context.Background(), st, newConvertEngine(t), convertOpts{}, &out)
	if err == nil || len(st.updates) != 0 {
		t.Fatalf("err=%v updates=%d\n%s", err, len(st.updates), out.String())
	}
	if !strings.Contains(out.String(), "changed since it was read") {
		t.Errorf("out = %s", out.String())
	}
}

// A raw-SQL writer (link rewrite) bumps updated_at but not version; the page
// must still not be overwritten with the pre-rewrite snapshot.
func TestConvertPagesSkipsPageTouchedBySQLSinceRead(t *testing.T) {
	st := &fakeConvertStore{rows: []convertRow{rowWith(convertSections)}, touch: time.Second}
	var out bytes.Buffer
	err := convertPages(context.Background(), st, newConvertEngine(t), convertOpts{}, &out)
	if err == nil || len(st.updates) != 0 {
		t.Fatalf("err=%v updates=%d\n%s", err, len(st.updates), out.String())
	}
	if !strings.Contains(out.String(), "changed since it was read") {
		t.Errorf("out = %s", out.String())
	}
}

// A dry run validates the doc like a real run: a doc the validator rejects
// must not pass as "0 diffs".
func TestConvertPagesDryRunValidatesDoc(t *testing.T) {
	attrs := `{"page_sections":[{"id":"a","type":"` + strings.Repeat("x", 65) + `","sort_order":0,"content":{}}]}`
	st := &fakeConvertStore{rows: []convertRow{rowWith(attrs)}}
	var out bytes.Buffer
	err := convertPages(context.Background(), st, newConvertEngine(t), convertOpts{DryRun: true}, &out)
	if err == nil || !strings.Contains(out.String(), "error:") {
		t.Fatalf("err=%v\n%s", err, out.String())
	}
}
