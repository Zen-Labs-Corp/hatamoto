package app

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/wpimport"
)

func acfCLIResult() *wpimport.Result {
	return &wpimport.Result{
		RunNumber: 2,
		ACF: &wpimport.ACFResult{Groups: 1, Fields: []wpimport.ACFFieldResult{
			{Group: "Listing", Label: "Price", Type: "number", Target: "listing", Status: "mapped"},
		}},
		Forms: &wpimport.FormsResult{Found: 1, Forms: []wpimport.FormResult{{Title: "Contact", Slug: "contact", Status: "imported", Fields: 3}}},
	}
}

func TestPrintImportResultACF(t *testing.T) {
	var b bytes.Buffer
	printImportResult(&b, acfCLIResult(), time.Second, false)
	for _, want := range []string{"Custom fields (ACF)", "Run 2: nothing new, 0 changed", "Forms (Contact Form 7)"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, b.String())
		}
	}
	b.Reset()
	printImportResult(&b, &wpimport.Result{}, time.Second, false)
	if strings.Contains(b.String(), "Custom fields") || strings.Contains(b.String(), "WordPress import report") {
		t.Errorf("a result without ACF printed a section:\n%s", b.String())
	}
}

func TestPrintImportResultACFJSON(t *testing.T) {
	var b bytes.Buffer
	printImportResult(&b, acfCLIResult(), time.Second, true)
	for _, want := range []string{`"acf"`, `"forms"`} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("JSON lacks %s:\n%s", want, b.String())
		}
	}
}
