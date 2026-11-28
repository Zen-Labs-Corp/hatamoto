package wpimport

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
)

var update = flag.Bool("update", false, "rewrite golden files")

func reportFixture() *Result {
	return &Result{
		RunNumber: 2,
		ACF: &ACFResult{Groups: 2, Fields: []ACFFieldResult{
			{Group: "Listing", Label: "Price", Type: "number", Target: "listing", Status: "mapped"},
			{Group: "Listing", Label: "Phone", Type: "text", Target: "listing", Status: "mapped"},
			{Group: "Listing", Label: "Photo", Type: "image", Target: "listing", Status: "mapped"},
			{Group: "Listing", Label: "Website", Type: "link", Target: "listing", Status: "mapped"},
			{Group: "Listing", Label: "Staff", Type: "repeater", Target: "listing", Status: "mapped", Rows: 41},
			{Group: "Listing", Label: "Related listings", Type: "relationship", Target: "listing", Status: "mapped", Links: 38},
			{Group: "Listing", Label: "Gallery", Type: "gallery", Target: "listing", Status: "mapped"},
			{Group: "Sections", Label: "Page sections", Type: "flexible_content", Target: "page", Status: "mapped", Items: 6, Rows: 14},
			{Group: "Sections", Label: "Nested blocks", Type: "flexible_content", Target: "page", Status: "partly",
				Note: "nested repeaters and flexible content inside rows are stored as text"},
			{Group: "Sections", Label: "Intro", Type: "text", Target: "page", Status: "partly",
				Note: "values kept as data, built-in types cannot gain fields"},
			{Group: "Sections", Label: "Map", Type: "google_map", Target: "page", Status: "skipped", Note: "a property map of another type exists; field skipped"},
		}},
		Forms: &FormsResult{Found: 2, MailNotImported: true, Forms: []FormResult{
			{Title: "Contact", Slug: "contact", Status: "imported", Fields: 5},
			{Title: "Quote request", Slug: "quote-request", Status: "partly", Fields: 4, Dropped: []string{"1 conditional rule not carried", "quiz dropped"}},
		}},
	}
}

func TestReportTextGolden(t *testing.T) {
	var b bytes.Buffer
	WriteReportText(&b, reportFixture())
	path := filepath.Join("testdata", "golden", "report-acf-cf7.txt")
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if b.String() != string(want) {
		t.Errorf("report text differs from %s (run with -update)\ngot:\n%s\nwant:\n%s", path, b.String(), want)
	}
}

func TestReportTextNothingWithoutACFOrForms(t *testing.T) {
	var b bytes.Buffer
	WriteReportText(&b, &Result{RunNumber: 3, Created: 4})
	if b.Len() != 0 {
		t.Errorf("wrote %q", b.String())
	}
	if ReportSections(nil) != nil {
		t.Error("nil result has sections")
	}
}

func TestReportHeadline(t *testing.T) {
	r := &Result{RunNumber: 2, Created: 1, ACF: &ACFResult{FieldsAdded: 1, RelationsChanged: 1}, Forms: &FormsResult{Created: 1}}
	if got := Headline(r); got != "Run 2: 3 new, 1 changed" {
		t.Errorf("got %q", got)
	}
	if got := Headline(&Result{ACF: &ACFResult{}}); got != "This run: nothing new, 0 changed" {
		t.Errorf("got %q", got)
	}
}

func TestReportOldStoredResult(t *testing.T) {
	var r Result
	if err := jsonx.Unmarshal([]byte(`{"run_id":"00000000-0000-0000-0000-000000000000","created":3}`), &r, jsonx.StoredLoose); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	WriteReportText(&b, &r)
	if b.Len() != 0 || r.ACF != nil || r.Forms != nil {
		t.Errorf("old result gained a report: %q", b.String())
	}
}
